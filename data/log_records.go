package data

// 数据内存索引，描述数据在磁盘上的位置
type LogRecordPos struct {
	//文件 id，偏移，
	PosID  uint32
	Offset uint64 //表示磁盘的哪个位置
}

type LogRecord struct {
}
