package data

import (
	"fmt"
	"hash/crc32"
	"io"
	"nova-kv/fio"
	"path/filepath"
)

const (
	DataFileNameSuffix = ".data"
)

// DataFile 数据文件
type DataFile struct {
	FileId    uint32
	WriteOff  int64
	IoManager fio.IOManager
}

// NewDataFile 创建/打开一个数据文件
func NewDataFile(dirPath string, fileId uint32) (*DataFile, error) {
	fileName := GetDataFileName(dirPath, fileId)
	ioManager, err := fio.NewFileIO(fileName)
	if err != nil {
		return nil, err
	}
	return &DataFile{
		FileId:    fileId,
		WriteOff:  0,
		IoManager: ioManager,
	}, nil
}

// GetDataFileName 构造数据文件完整路径
func GetDataFileName(dirPath string, fileId uint32) string {
	return filepath.Join(dirPath, fmt.Sprintf("%09d", fileId)+DataFileNameSuffix)
}

// ReadLogRecord 从指定偏移量读取一条 LogRecord
//
// 流程：
//  1. 边界检查：offset 不能超出文件大小，读不到 maxLogRecordHeaderSize 时只读到文件末尾
//  2. 解码 header，拿到 headerSize、keySize、valueSize、seqNo、存储的 CRC
//  3. 读取 key + value
//  4. 将 header(跳过 CRC 前4字节) + key + value 拼成一个 buffer，重算 CRC 并比对
func (df *DataFile) ReadLogRecord(offset int64) (*LogRecord, int64, error) {
	// 1. 边界检查
	fileSize, err := df.IoManager.Size()
	if err != nil {
		return nil, 0, err
	}
	if offset >= fileSize {
		return nil, 0, io.EOF
	}
	headerReadSize := int64(maxLogRecordHeaderSize)
	if offset+headerReadSize > fileSize {
		headerReadSize = fileSize - offset
	}

	// 2. 读取 header
	headerBuf := make([]byte, headerReadSize)
	n, err := df.IoManager.Read(headerBuf, offset)
	if err == io.EOF && n == 0 {
		return nil, 0, io.EOF
	}
	if err != nil && err != io.EOF {
		return nil, 0, err
	}

	header, headerSize, err := DecodeLogRecordHeader(headerBuf[:n])
	if err != nil {
		return nil, 0, err
	}
	if header == nil || headerSize == 0 {
		return nil, 0, fmt.Errorf("decode log record header failed")
	}

	// 3. 读取 key + value
	kvSize := int64(header.keySize + header.valueSize)
	kvBuf := make([]byte, kvSize)
	if kvSize > 0 {
		_, err = df.IoManager.Read(kvBuf, offset+headerSize)
		if err != nil && err != io.EOF {
			return nil, 0, err
		}
	}

	// 4. 构建 recordData 并验证 CRC
	// recordData = Type + KeySize + ValueSize + SeqNo + Key + Value（不含 CRC）
	recordData := append([]byte{}, headerBuf[4:int(headerSize)]...)
	recordData = append(recordData, kvBuf...)

	storedCRC := header.crc
	computedCRC := crc32.ChecksumIEEE(recordData)
	if storedCRC != computedCRC {
		return nil, 0, fmt.Errorf("CRC mismatch: stored=%d, computed=%d", storedCRC, computedCRC)
	}

	// 5. 组装返回（包含 SeqNo）
	record := &LogRecord{
		Type:  header.recordType,
		SeqNo: header.seqNo,
	}
	if kvSize > 0 {
		record.Key = kvBuf[:header.keySize]
		record.Value = kvBuf[header.keySize:]
	}

	size := headerSize + kvSize
	return record, size, nil
}

// Write 写入字节数据到文件末尾
func (df *DataFile) Write(buf []byte) error {
	n, err := df.IoManager.Write(buf)
	if err != nil {
		return err
	}
	df.WriteOff += int64(n)
	return nil
}

// Sync 强制刷盘
func (df *DataFile) Sync() error {
	return df.IoManager.Sync()
}

// Close 关闭文件
func (df *DataFile) Close() error {
	return df.IoManager.Close()
}
