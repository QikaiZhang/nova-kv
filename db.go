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

	// nextSeqNo 下一个事务序列号（仅由 WriteBatch.Commit 递增）
	// 用于恢复时判断哪些 batch 已完整提交
	nextSeqNo uint64
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
		nextSeqNo:  1, // 从 1 开始，0 留给非事务写入
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

	// 2. 构造 LogRecord（SeqNo=0 表示非事务写入）
	logRecord := &data.LogRecord{
		Key:  key,
		Value: value,
		Type: data.LogRecordNormal,
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
		Key: key,
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

// =============================================================================
// Close & Sync
// =============================================================================

// Close 关闭数据库，释放所有文件资源
// 先 Sync 保证数据持久化，再依次关闭活跃文件和旧文件
func (db *DB) Close() error {
	db.mu.Lock()
	defer db.mu.Unlock()

	// 先刷活跃文件
	if db.activeFile != nil {
		if err := db.activeFile.Sync(); err != nil {
			return fmt.Errorf("failed to sync active file before close: %w", err)
		}
		if err := db.activeFile.Close(); err != nil {
			return fmt.Errorf("failed to close active file: %w", err)
		}
	}

	// 关闭旧文件（它们写满时已经持久化过）
	for fid, df := range db.olderFiles {
		if err := df.Close(); err != nil {
			return fmt.Errorf("failed to close older file %d: %w", fid, err)
		}
	}

	return nil
}

// Strongly Recommended to Handwrite
// Sync 强制将活跃文件刷盘
// 注意：只对活跃文件操作——旧文件在写满切换时已经 Sync 过了，不需要再次刷
func (db *DB) Sync() error {
	db.mu.Lock()
	defer db.mu.Unlock()

	if db.activeFile != nil {
		return db.activeFile.Sync()
	}
	return nil
}

// =============================================================================
// ListKeys & Fold —— 遍历数据
// =============================================================================

// Strongly Recommended to Handwrite
// ListKeys 返回数据库中所有的 key
// 复用索引迭代器，直接从内存索引中获取（索引全在内存，不需要读磁盘）
func (db *DB) ListKeys() [][]byte {
	db.mu.RLock()
	defer db.mu.RUnlock()

	iter := db.index.NewIterator(index.IteratorOptions{})
	defer iter.Close()

	var keys [][]byte
	for iter.Rewind(); iter.Valid(); iter.Next() {
		key := iter.Key()
		if key == nil {
			continue
		}
		// 拷贝 key，避免外部修改影响迭代器内部数据
		keyCopy := make([]byte, len(key))
		copy(keyCopy, key)
		keys = append(keys, keyCopy)
	}
	return keys
}

// Strongly Recommended to Handwrite
// Fold 遍历数据库中所有的 key-value 对
// fn 返回 false 时提前终止遍历
// 注意：value 需要从磁盘读取（索引只存位置，不缓存 value）
func (db *DB) Fold(fn func(key []byte, value []byte) bool) error {
	db.mu.RLock()
	defer db.mu.RUnlock()

	iter := db.index.NewIterator(index.IteratorOptions{})
	defer iter.Close()

	for iter.Rewind(); iter.Valid(); iter.Next() {
		key := iter.Key()
		pos := iter.Value()
		if key == nil || pos == nil {
			continue
		}

		// 根据位置读磁盘
		df, err := db.getDataFile(pos.Fid)
		if err != nil {
			return fmt.Errorf("fold: get data file %d: %w", pos.Fid, err)
		}

		logRecord, _, err := df.ReadLogRecord(pos.Offset)
		if err != nil {
			return fmt.Errorf("fold: read log record at offset %d: %w", pos.Offset, err)
		}

		// 跳过已删除的墓碑记录（理论不应该出现在索引中，防御性检查）
		if logRecord.Type == data.LogRecordDeleted {
			continue
		}

		if !fn(key, logRecord.Value) {
			break
		}
	}
	return nil
}

// =============================================================================
// 内部辅助方法
// =============================================================================

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
		// 先把当前文件持久化
		if err := db.activeFile.Sync(); err != nil {
			return nil, err
		}

		// 当前文件归档
		db.olderFiles[db.activeFile.FileId] = db.activeFile

		// 创建新的活跃文件
		if err := db.setActiveDataFile(); err != nil {
			return nil, err
		}
	}

	// 4. 记录当前偏移
	writeOff := db.activeFile.WriteOff

	// 5. 写入磁盘
	if err := db.activeFile.Write(encRecord); err != nil {
		return nil, err
	}

	// 6. 根据配置决定是否立即刷盘
	if db.options.SyncWrites {
		if err := db.activeFile.Sync(); err != nil {
			return nil, err
		}
	}

	// 7. 构造并返回位置信息
	pos := &data.LogRecordPos{
		Fid:    db.activeFile.FileId,
		Offset: writeOff,
		Size:   uint32(size),
	}
	return pos, nil
}

// appendLogRecordWithPos 追加写入一条日志记录（不更新 index），返回位置
// 供 WriteBatch 使用，写完后由 batch 统一更新索引
func (db *DB) appendLogRecordWithPos(logRecord *data.LogRecord) (*data.LogRecordPos, error) {
	return db.appendLogRecord(logRecord)
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
// 以及事务 SeqNo 的恢复逻辑
//
// loadIndexFromDataFiles 从所有数据文件重建内存索引
// 从最老文件到最新文件逐条读取，同一 key 后处理的值覆盖先处理的
// 遇到无法解码的记录时放弃当前文件剩余部分，继续下一个文件（保守安全策略）
//
// SeqNo 恢复逻辑（强烈建议手写理解）：
//  1. SeqNo==0 的记录：普通 Put/Delete，直接应用到索引
//  2. SeqNo>0 的记录：WriteBatch 写入，暂存到 pendingBatch 中，按 SeqNo 分组
//  3. 遇到 LogRecordTxnFinished（携带 SeqNo=N）：将该 SeqNo 对应的暂存记录应用到索引，并清理
//  4. 无论是否为 TxnFinished，都跟踪 maxSeqNo
//  5. 遍历结束后，pendingBatch 中残留的记录 = 未完成的事务，丢弃（后续 merge 清理）
func (db *DB) loadIndexFromDataFiles() error {
	// Invariant:
	// 如果数据库存在任何数据文件，activeFile 一定非 nil。
	// 因此 activeFile == nil 表示无需恢复索引。
	if db.activeFile == nil && len(db.olderFiles) == 0 {
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

	// pendingBatch: 按 SeqNo 分组暂存未完成事务的记录
	// map[seqNo] -> []pendingInfo
	type pendingInfo struct {
		key   []byte
		pos   *data.LogRecordPos
		rtype data.LogRecordType
	}
	pendingBatch := make(map[uint64][]pendingInfo)
	var maxSeqNo uint64

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

			// 跟踪最大 SeqNo（含 TxnFinished 的 SeqNo）
			if logRecord.SeqNo > maxSeqNo {
				maxSeqNo = logRecord.SeqNo
			}

			// 检查是否为事务完成标记
			if logRecord.Type == data.LogRecordTxnFinished {
				// 该 SeqNo 的 batch 已完整提交，将对应暂存记录应用到索引
				if records, ok := pendingBatch[logRecord.SeqNo]; ok {
					for _, p := range records {
						if p.rtype == data.LogRecordNormal {
							db.index.Put(p.key, p.pos)
						} else if p.rtype == data.LogRecordDeleted {
							db.index.Delete(p.key)
						}
					}
					delete(pendingBatch, logRecord.SeqNo)
				}
				offset += size
				continue
			}

			// 构建索引位置
			pos := &data.LogRecordPos{
				Fid:    uint32(fid),
				Offset: offset,
				Size:   uint32(size),
			}

			if logRecord.SeqNo > 0 {
				// 事务写入：按 SeqNo 分组暂存，等对应的 TxnFinished 标记
				pendingBatch[logRecord.SeqNo] = append(pendingBatch[logRecord.SeqNo], pendingInfo{
					key:   logRecord.Key,
					pos:   pos,
					rtype: logRecord.Type,
				})
			} else {
				// 普通写入：直接应用
				if logRecord.Type == data.LogRecordNormal {
					db.index.Put(logRecord.Key, pos)
				} else if logRecord.Type == data.LogRecordDeleted {
					db.index.Delete(logRecord.Key)
				}
				// 未知记录类型被静默忽略（向前兼容）
			}

			offset += size
		}
	}

	// 恢复 nextSeqNo：在最大 SeqNo 基础上 +1
	// 如果没有任何事务记录，保持默认值 1
	if maxSeqNo > 0 {
		db.nextSeqNo = maxSeqNo + 1
	}

	// 遍历结束后 pendingBatch 中仍有记录 = 未完成的事务（脏数据），丢弃
	// 后续 merge 时这些垃圾数据会被清理

	// 如果有损坏记录，返回第一个作为聚合错误，备注：提前设计后续待优化
	if len(corruptedRecords) > 0 {
		return corruptedRecords[0]
	}

	return nil
}
