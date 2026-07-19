package data

import (
	"fmt"
	"io"
	"nova-kv/fio"
	"path/filepath"
)

const (
	// DataFileNameSuffix 数据文件后缀
	DataFileNameSuffix = ".data"
)

// DataFile 数据文件
type DataFile struct {
	FileId   uint32        // 文件 ID
	WriteOff int64         // 当前写入偏移（仅 active file 有效）
	IoManager fio.IOManager // IO 管理器
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

// Strongly Recommended to Handwrite
// ReadLogRecord 从指定偏移量读取一条 LogRecord
// 注意：ReadAt 在文件末尾同时返回数据和 io.EOF，需区分"读 0 字节+EOF"（真结尾）vs"读 N 字节+EOF"（最后一条记录）
func (df *DataFile) ReadLogRecord(offset int64) (*LogRecord, int64, error) {
	headerBuf := make([]byte, maxLogRecordHeaderSize)
	n, err := df.IoManager.Read(headerBuf, offset)

	// 读到 0 字节 + EOF = 真正文件末尾
	if err == io.EOF && n == 0 {
		return nil, 0, io.EOF
	}
	// 其他 I/O 错误（EOF 除外）= 真正的读取失败
	if err != nil && err != io.EOF {
		return nil, 0, err
	}

	// 即使 n < maxLogRecordHeaderSize，只要 header 能解码就继续
	header, headerSize := DecodeLogRecordHeader(headerBuf[:n])
	if header == nil || headerSize == 0 {
		return nil, 0, fmt.Errorf("decode log record header failed")
	}

	record := &LogRecord{
		Type: header.recordType,
	}

	// 读取 key 和 value（EOF 允许，因为可能刚好读到记录末尾）
	kvSize := int(header.keySize + header.valueSize)
	if kvSize > 0 {
		kvBuf := make([]byte, kvSize)
		_, err = df.IoManager.Read(kvBuf, offset+headerSize)
		if err != nil && err != io.EOF {
			return nil, 0, err
		}
		record.Key = kvBuf[:header.keySize]
		record.Value = kvBuf[header.keySize:]
	}

	size := headerSize + int64(kvSize)
	return record, size, nil
}

// Write 写入字节数据到文件末尾
func (df *DataFile) Write(buf []byte) error {
	n, err := df.IoManager.Write(buf)
	if err != nil {
		return err
	}
	// 更新写入偏移
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
