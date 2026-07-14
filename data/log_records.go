package data

import "encoding/binary"

// LogRecordType 日志记录类型
type LogRecordType = byte

const (
	// LogRecordNormal 正常写入的记录
	LogRecordNormal LogRecordType = iota + 1
	// LogRecordDeleted 墓碑记录，表示 key 已被删除
	LogRecordDeleted
)

// LogRecord 磁盘上的日志记录（内存表示）
// 磁盘编码格式：
// +-------+--------+-----------+-------------+-----+-------+
// | CRC   |  Type  |  KeySize  |  ValueSize  | Key | Value |
// +-------+--------+-----------+-------------+-----+-------+
//   4B      1B       4B(变长)    4B(变长)       变长   变长
type LogRecord struct {
	Key   []byte
	Value []byte
	Type  LogRecordType
}

// LogRecordPos 索引指向的位置信息
type LogRecordPos struct {
	Fid    uint32 // 文件 ID
	Offset int64  // 在文件中的偏移
	Size   uint32 // 记录在磁盘上的总大小（用于 merge 统计）
}

// logRecordHeader LogRecord 的固定头部
// CRC(4) + Type(1) + KeySize(4) + ValueSize(4) = 13 字节
const maxLogRecordHeaderSize = 4 + 1 + binary.MaxVarintLen32*2

// EncodeLogRecord 将 LogRecord 编码为二进制字节流
// 返回编码后的字节数组和实际头部大小
func EncodeLogRecord(logRecord *LogRecord) ([]byte, int64) {
	// 预分配 header 空间（最大可能值）
	header := make([]byte, maxLogRecordHeaderSize)

	// 第 5 字节开始写 Type（前 4 字节留着放 CRC）
	header[4] = logRecord.Type

	var index = 5
	// 写入 KeySize（变长编码，节省空间）
	index += binary.PutVarint(header[index:], int64(len(logRecord.Key)))
	// 写入 ValueSize
	index += binary.PutVarint(header[index:], int64(len(logRecord.Value)))

	// 总大小 = header 实际大小 + key + value
	var size = index + len(logRecord.Key) + len(logRecord.Value)
	encBytes := make([]byte, size)

	// 拷贝 header 部分（跳过 CRC，先不填）
	copy(encBytes[:index], header[:index])

	// 拷贝 key 和 value
	copy(encBytes[index:index+len(logRecord.Key)], logRecord.Key)
	copy(encBytes[index+len(logRecord.Key):], logRecord.Value)

	// 计算 CRC（从 Type 开始到末尾）
	// TODO: 后续补充 CRC 校验，先留空
	// crc := crc32.ChecksumIEEE(encBytes[4:])
	// binary.LittleEndian.PutUint32(encBytes[:4], crc)

	return encBytes, int64(size)
}

// decodeLogRecordHeader 从字节数组中解码 LogRecord 的 header 部分
// 返回 header 信息和 header 实际占用的字节数
func DecodeLogRecordHeader(buf []byte) (*logRecordHeader, int64) {
	if len(buf) <= 4 {
		return nil, 0
	}

	header := &logRecordHeader{
		// crc:  binary.LittleEndian.Uint32(buf[:4]),
		recordType: buf[4],
	}

	var index = 5
	// 读取 KeySize
	keySize, n := binary.Varint(buf[index:])
	index += n
	header.keySize = uint32(keySize)

	// 读取 ValueSize
	valueSize, n := binary.Varint(buf[index:])
	index += n
	header.valueSize = uint32(valueSize)

	return header, int64(index)
}

// logRecordHeader 解码后的 header 信息（内部使用）
type logRecordHeader struct {
	crc        uint32
	recordType LogRecordType
	keySize    uint32
	valueSize  uint32
}
