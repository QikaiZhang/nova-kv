package data

// 数据内存索引，描述数据在磁盘上的位置
type LogRecordPos struct {
	//文件 id，偏移，
	PosID  uint32
	Offset uint64
}

type LogRecord struct {
}
