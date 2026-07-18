package novakv

import (
	"errors"
	"fmt"
	"io"
	"nova-kv/data"
	"nova-kv/index"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// DB 数据库主结构体
type DB struct {
	options Options
	mu      *sync.RWMutex

	// activeFile 当前活跃文件，可写
	activeFile *data.DataFile
	// olderFiles 已封存的旧文件，只读
	olderFiles map[uint32]*data.DataFile

	// index 内存索引
	index index.Indexer // 内存索引，接口类型，方便后续切换不同索引实现
}

// Strongly Recommended to Handwrite
// validateOptions 校验启动配置：非空目录、合法文件大小、已知索引类型
// 在 Open 最开始执行，失败不产生任何磁盘副作用
func validateOptions(opts *Options) error {
	if opts.DirPath == "" {
		return ErrOptionsDirPathEmpty
	}
	if opts.DataFileSize <= 0 {
		return ErrOptionsDataFileSizeTooSmall
	}
	if opts.IndexType != BTree {
		return ErrOptionsIndexTypeUnknown
	}
	return nil
}

// Open 打开/创建一个数据库实例
func Open(options Options) (*DB, error) {
	// 1. 参数校验（优先执行，不做任何磁盘操作）
	if err := validateOptions(&options); err != nil {
		return nil, err
	}

	// 2. 确保数据目录存在
	if err := os.MkdirAll(options.DirPath, os.ModePerm); err != nil {
		return nil, err
	}

	// 3. 根据配置选择索引实现
	var idx index.Indexer
	switch options.IndexType {
	case BTree:
		idx = index.NewBTree()
	default:
		return nil, ErrOptionsIndexTypeUnknown
	}

	// 4. 初始化 DB 结构体
	db := &DB{
		options:    options,
		mu:         new(sync.RWMutex),
		olderFiles: make(map[uint32]*data.DataFile),
		index:      idx,
	}

	// 5. 加载数据目录下的所有数据文件
	if err := db.loadDataFiles(); err != nil {
		return nil, err
	}

	// 6. 从数据文件中构建索引
	if err := db.loadIndexFromDataFiles(); err != nil {
		// 索引重建时遇到无法解析的记录（磁盘损坏或文件尾部空隙）
		// 已恢复的索引数据仍然可用，不应阻止 DB 启动
		var corrupted *CorruptedRecordError
		if !errors.As(err, &corrupted) {
			return nil, err
		}
		// corrupted records: return DB with partial recovery
	}

	return db, nil
}

// Put 写入 key-value
// 核心流程：
//  1. 参数校验
//  2. 构造 LogRecord
//  3. 追加写入 active file（超过阈值则切换新文件）
//  4. （可选）刷盘
//  5. 更新内存索引
func (db *DB) Put(key []byte, value []byte) error {
	// 1. 参数校验
	if len(key) == 0 {
		return ErrKeyIsEmpty
	}

	db.mu.Lock()
	defer db.mu.Unlock()

	// 2. 构造 LogRecord
	logRecord := &data.LogRecord{
		Key:   key,
		Value: value,
		Type:  data.LogRecordNormal,
	}

	// 3. 追加写入
	pos, err := db.appendLogRecord(logRecord)
	if err != nil {
		return err
	}

	// 4. 更新索引
	if ok := db.index.Put(key, pos); !ok {
		// 注意：写磁盘成功但索引更新失败，数据不会丢，重启可恢复
		// 这里返回错误让调用方知道
		return nil // TODO: 定义索引更新失败的错误
	}

	return nil
}

// Get 根据 key 读取 value
// 核心流程：
//  1. 参数校验
//  2. 查内存索引，拿到 (FileID, Offset)
//  3. 根据 FileID 找到对应 DataFile
//  4. 从 Offset 读取 LogRecord
//  5. 判断记录类型（Normal 还是 Deleted 墓碑）
//  6. 返回 value
func (db *DB) Get(key []byte) ([]byte, error) {
	if len(key) == 0 {
		return nil, ErrKeyIsEmpty
	}

	db.mu.RLock()
	defer db.mu.RUnlock()

	// 2. 查索引
	pos := db.index.Get(key)
	if pos == nil {
		return nil, ErrKeyNotFound
	}

	// 3. 找到对应的数据文件
	df, err := db.getDataFile(pos.Fid)
	if err != nil {
		return nil, err
	}

	// 4. 读取记录
	logRecord, _, err := df.ReadLogRecord(pos.Offset)
	if err != nil {
		return nil, err
	}

	// 5. 判断是否是墓碑
	if logRecord.Type == data.LogRecordDeleted {
		return nil, ErrKeyNotFound
	}

	return logRecord.Value, nil
}

// Delete 删除 key
// 实现方式：写入一条墓碑记录，然后从索引中移除
func (db *DB) Delete(key []byte) error {
	if len(key) == 0 {
		return ErrKeyIsEmpty
	}

	db.mu.Lock()
	defer db.mu.Unlock()

	// 先查索引，不存在直接返回
	if pos := db.index.Get(key); pos == nil {
		return nil
	}

	// 写入墓碑记录
	logRecord := &data.LogRecord{
		Key:  key,
		Type: data.LogRecordDeleted,
	}
	_, err := db.appendLogRecord(logRecord)
	if err != nil {
		return err
	}

	// 从索引中删除
	db.index.Delete(key)
	return nil
}

// appendLogRecord 追加写入一条日志记录
// 返回记录在文件中的位置信息
func (db *DB) appendLogRecord(logRecord *data.LogRecord) (*data.LogRecordPos, error) {
	// 1. 检查 active file 是否存在，不存在则初始化
	if db.activeFile == nil {
		if err := db.setActiveDataFile(); err != nil {
			return nil, err
		}
	}

	// 2. 编码 LogRecord
	encRecord, size := data.EncodeLogRecord(logRecord)

	// 3. 检查当前 active file 写入后是否超过阈值
	if db.activeFile.WriteOff+size > db.options.DataFileSize {
		// 先把当前文件刷盘封存
		if err := db.activeFile.Sync(); err != nil {
			return nil, err
		}
		// 移入 olderFiles
		db.olderFiles[db.activeFile.FileId] = db.activeFile
		// 打开新的 active file
		if err := db.setActiveDataFile(); err != nil {
			return nil, err
		}
	}

	// 4. 记录写入偏移
	writeOff := db.activeFile.WriteOff

	// 5. 写入文件
	if err := db.activeFile.Write(encRecord); err != nil {
		return nil, err
	}

	// 6. 配置决定是否每次刷盘
	if db.options.SyncWrites {
		if err := db.activeFile.Sync(); err != nil {
			return nil, err
		}
	}

	// 7. 返回位置信息
	pos := &data.LogRecordPos{
		Fid:    db.activeFile.FileId,
		Offset: writeOff,
		Size:   uint32(size),
	}
	return pos, nil
}

// setActiveDataFile 创建/设置新的活跃文件
// 新文件 ID = 当前最大 ID + 1
func (db *DB) setActiveDataFile() error {
	var initialFileId uint32 = 0
	if db.activeFile != nil {
		initialFileId = db.activeFile.FileId + 1
	}

	dataFile, err := data.NewDataFile(db.options.DirPath, initialFileId)
	if err != nil {
		return err
	}
	db.activeFile = dataFile
	return nil
}

// getDataFile 根据文件 ID 获取对应的数据文件
func (db *DB) getDataFile(fid uint32) (*data.DataFile, error) {
	// 先看是不是活跃文件
	if db.activeFile.FileId == fid {
		return db.activeFile, nil
	}
	// 再看旧文件
	if df, ok := db.olderFiles[fid]; ok {
		return df, nil
	}
	return nil, ErrDataFileNotFound
}

// Strongly Recommended to Handwrite：理解启动恢复流程、文件命名约定、活跃文件判定逻辑
// loadDataFiles 扫描目录，按 9 位零填充数字筛选 .data 文件，ID 最大的为 active file
// 文件 ID 不要求连续——Merge 或手动清理可能留下间隔
func (db *DB) loadDataFiles() error {
	dirEntries, err := os.ReadDir(db.options.DirPath)
	if err != nil {
		return err
	}

	var fileIds []int
	// 文件名格式：恰好 9 位数字 + .data（如 000000001.data）
	// 非此格式静默跳过（备份文件、临时文件、非法命名）
	for _, entry := range dirEntries {
		if !strings.HasSuffix(entry.Name(), data.DataFileNameSuffix) {
			continue
		}

		// TrimSuffix 去后缀 + 长度校验，比 Split(".")[0] 更严谨
		trimmed := strings.TrimSuffix(entry.Name(), data.DataFileNameSuffix)
		if len(trimmed) != 9 {
			continue
		}
		fileId, err := strconv.Atoi(trimmed)
		if err != nil || fileId < 0 {
			continue
		}
		fileIds = append(fileIds, fileId)
	}

	sort.Ints(fileIds)

	// 依次打开所有数据文件
	for i, fid := range fileIds {
		dataFile, err := data.NewDataFile(db.options.DirPath, uint32(fid))
		if err != nil {
			return fmt.Errorf("failed to open data file %d: %w", fid, err)
		}
		if i == len(fileIds)-1 {
			// 最大 ID 的是活跃文件
			db.activeFile = dataFile
		} else {
			// 其余是旧文件
			db.olderFiles[uint32(fid)] = dataFile
		}
	}

	// 如果没有任何文件（第一次启动），初始化一个空的活跃文件
	if db.activeFile == nil {
		return db.setActiveDataFile()
	}

	// 设置活跃文件的 WriteOff，确保重启后追加写入而非覆盖
	activeFilePath := data.GetDataFileName(db.options.DirPath, db.activeFile.FileId)
	stat, err := os.Stat(activeFilePath)
	if err != nil {
		return fmt.Errorf("failed to stat active file %d: %w", db.activeFile.FileId, err)
	}
	db.activeFile.WriteOff = stat.Size()

	return nil
}

// Strongly Recommended to Handwrite：理解磁盘是真相来源、索引可重建、墓碑记录的处理
// loadIndexFromDataFiles 从所有数据文件重建内存索引
// 从最老文件到最新文件逐条读取，同一 key 后处理的值覆盖先处理的
// 遇到无法解码的记录时放弃当前文件剩余部分，继续下一个文件（保守安全策略）
func (db *DB) loadIndexFromDataFiles() error {
	// 没有文件，第一次启动
	if db.activeFile == nil {
		return nil
	}

	// 收集所有需要遍历的文件 ID，按从小到大顺序
	var fileIds []int
	for fid := range db.olderFiles {
		fileIds = append(fileIds, int(fid))
	}
	fileIds = append(fileIds, int(db.activeFile.FileId))
	sort.Ints(fileIds)

	var corruptedRecords []*CorruptedRecordError

	// 遍历每个文件，逐条读取并更新索引
	for _, fid := range fileIds {
		var dataFile *data.DataFile
		if uint32(fid) == db.activeFile.FileId {
			dataFile = db.activeFile
		} else {
			dataFile = db.olderFiles[uint32(fid)]
		}

		var offset int64 = 0
		for {
			logRecord, size, err := dataFile.ReadLogRecord(offset)
			if err != nil {
				if err == io.EOF {
					break
				}
				// 遇到非 EOF 错误（解码失败、数据损坏等）
				// 记录损坏位置，跳过当前文件剩余部分，继续下一个文件
				corruptedRecords = append(corruptedRecords, &CorruptedRecordError{
					FileId: uint32(fid),
					Offset: offset,
					Cause:  err,
				})
				break
			}

			// 构建索引位置
			pos := &data.LogRecordPos{
				Fid:    uint32(fid),
				Offset: offset,
				Size:   uint32(size),
			}

			// 根据记录类型更新索引
			// 未知记录类型被静默忽略（向前兼容）
			if logRecord.Type == data.LogRecordNormal {
				db.index.Put(logRecord.Key, pos)
			} else if logRecord.Type == data.LogRecordDeleted {
				db.index.Delete(logRecord.Key)
			}

			offset += size
		}
	}

	// 如果有损坏记录，返回第一个作为聚合错误
	if len(corruptedRecords) > 0 {
		return corruptedRecords[0]
	}

	return nil
}
