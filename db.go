package novakv

import (
	"nova-kv/data"
	"nova-kv/index"
	"sync"
)

// DB 数据库主结构体
type DB struct {
	options    Options
	mu         *sync.RWMutex

	// activeFile 当前活跃文件，可写
	activeFile *data.DataFile
	// olderFiles 已封存的旧文件，只读
	olderFiles map[uint32]*data.DataFile

	// index 内存索引
	index index.Indexer // 内存索引，接口类型，方便后续切换不同索引实现
}

// Open 打开/创建一个数据库实例
func Open(options Options) (*DB, error) {
	// 1. 参数校验
	// TODO: 校验 DirPath、DataFileSize 合法性

	// 2. 初始化 DB 结构体
	db := &DB{
		options:    options,
		mu:         new(sync.RWMutex),
		olderFiles: make(map[uint32]*data.DataFile),
		index:      index.NewBTree(),
	}

	// 3. 加载数据目录下的所有数据文件
	if err := db.loadDataFiles(); err != nil {
		return nil, err
	}

	// 4. 从数据文件中构建索引
	// TODO: loadIndexFromDataFiles

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

// loadDataFiles 启动时加载所有数据文件
// TODO: 完整实现，需要遍历目录、按文件 ID 排序、区分 active/older
func (db *DB) loadDataFiles() error {
	// 1. 遍历数据目录，找到所有 .data 文件
	// 2. 从文件名中解析出 FileID
	// 3. 按 ID 从小到大排序
	// 4. 依次打开所有文件
	// 5. 最大 ID 的文件设为 activeFile，其余放入 olderFiles
	// 6. 设置 activeFile 的 WriteOff（文件当前大小）

	// 临时：初始化一个空文件，保证 Put 能跑
	return db.setActiveDataFile()
}
