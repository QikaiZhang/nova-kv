package data

import (
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
)

// LogRecordType 日志记录类型
type LogRecordType = byte

const (
	// LogRecordNormal 正常写入的记录
	LogRecordNormal LogRecordType = iota + 1
	// LogRecordDeleted 墓碑记录，表示 key 已被删除
	LogRecordDeleted
	// LogRecordTxnFinished 事务完成标记，标识一个 WriteBatch 已完整提交
	LogRecordTxnFinished
)

// LogRecord 磁盘上的日志记录（内存表示）
// 磁盘编码格式：
// +-------+--------+-----------+-------------+-----------+-----+-------+
// | CRC   |  Type  |  KeySize  |  ValueSize  |   SeqNo   | Key | Value |
// +-------+--------+-----------+-------------+-----------+-----+-------+
//
//	4B      1B       4B(变长)    4B(变长)      8B(变长)     变长   变长
type LogRecord struct {
	Key   []byte
	Value []byte
	Type  LogRecordType
	// SeqNo 事务序列号，全局递增
	// 0 表示非事务写入（直接 Put），>0 表示 WriteBatch 写入
	SeqNo uint64
}

// LogRecordPos 索引指向的位置信息
type LogRecordPos struct {
	Fid    uint32 // 文件 ID
	Offset int64  // 在文件中的偏移
	Size   uint32 // 记录在磁盘上的总大小（用于 merge 统计）
}

// logRecordHeader LogRecord 的固定头部
// CRC(4) + Type(1) + KeySize(varint) + ValueSize(varint) + SeqNo(varint) = 最大 4+1+5+5+10 = 25 字节
const maxLogRecordHeaderSize = 4 + 1 + binary.MaxVarintLen32*2 + binary.MaxVarintLen64

var (
	// ErrIncompleteHeader 头部数据不完整，通常是文件尾部写入中断导致
	ErrIncompleteHeader = errors.New("log record header is incomplete, possible truncated write at file tail")
)

// 强烈建议手写，你将收获：理解二进制协议设计、变长编码原理、内存布局计算
// EncodeLogRecord 将 LogRecord 编码为二进制字节流
// 返回编码后的字节数组和实际记录总大小
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
	// 写入 SeqNo
	index += binary.PutVarint(header[index:], int64(logRecord.SeqNo))

	// 总大小 = header 实际大小 + key + value
	var size = index + len(logRecord.Key) + len(logRecord.Value)
	encBytes := make([]byte, size)

	// 拷贝 header 部分（跳过 CRC，先不填）
	copy(encBytes[:index], header[:index])

	// 拷贝 key 和 value
	copy(encBytes[index:index+len(logRecord.Key)], logRecord.Key)
	copy(encBytes[index+len(logRecord.Key):], logRecord.Value)

	crc := crc32.ChecksumIEEE(encBytes[4:])
	binary.LittleEndian.PutUint32(encBytes[:4], crc)

	return encBytes, int64(size)
}

// 强烈建议手写，你将收获：理解二进制协议解析、变长解码、边界判断
// DecodeLogRecordHeader 从字节数组中解码 LogRecord 的 header 部分
// 返回 header 信息和 header 实际占用的字节数
func DecodeLogRecordHeader(buf []byte) (*logRecordHeader, int64, error) {
	if len(buf) <= 4 {
		return nil, 0, fmt.Errorf("buf长度异常可能被截断")
	}

	header := &logRecordHeader{
		crc:        binary.LittleEndian.Uint32(buf[:4]),
		recordType: buf[4],
	}

	var index = 5
	// 读取 KeySize
	keySize, n := binary.Varint(buf[index:])
	// n <= 0 说明 varint 编码被截断（文件尾部写入中断），不是合法的完整记录
	if n <= 0 {
		return nil, 0, fmt.Errorf("varint 编码被截断")
	}
	if keySize < 0 {
		return nil, 0, fmt.Errorf("kvSize非法")
	}

	index += n
	header.keySize = uint32(keySize)

	// 读取 ValueSize
	valueSize, n := binary.Varint(buf[index:])
	// 同上，截断的 varint 不可解码
	if n <= 0 {
		return nil, 0, fmt.Errorf("record可能被截断")
	}
	index += n
	header.valueSize = uint32(valueSize)

	// 读取 SeqNo（新增字段，兼容旧格式：截断时 SeqNo=0）
	seqNo, n := binary.Varint(buf[index:])
	if n <= 0 {
		// 旧格式没有 SeqNo 字段，截断时默认为 0
		header.seqNo = 0
	} else {
		index += n
		if seqNo < 0 {
			return nil, 0, fmt.Errorf("seqNo非法")
		}
		header.seqNo = uint64(seqNo)
	}

	// 返回 header，调用方负责验证 keySize+valueSize 不超过文件剩余字节
	return header, int64(index), nil
}

// logRecordHeader 解码后的 header 信息（内部使用）
type logRecordHeader struct {
	crc        uint32
	recordType LogRecordType
	keySize    uint32
	valueSize  uint32
	seqNo      uint64
}
