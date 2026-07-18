package data

import (
	"fmt"
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

// ReadLogRecord 根据偏移读取一条 LogRecord
// 返回记录本身、记录总大小
func (df *DataFile) ReadLogRecord(offset int64) (*LogRecord, int64, error) {
	// 先读 header（读最大可能的 header 大小，防止读少了）
	headerBuf := make([]byte, maxLogRecordHeaderSize)
	_, err := df.IoManager.Read(headerBuf, offset)
	if err != nil {
		return nil, 0, err
	}

	// 解码 header
	header, headerSize := DecodeLogRecordHeader(headerBuf)
	if header == nil || headerSize == 0 {
		return nil, 0, fmt.Errorf("decode log record header failed")
	}

	// 构造完整的 LogRecord
	record := &LogRecord{
		Type: header.recordType,
	}

	// 读取 key 和 value
	kvSize := int(header.keySize + header.valueSize)
	if kvSize > 0 {
		kvBuf := make([]byte, kvSize)
		_, err = df.IoManager.Read(kvBuf, offset+headerSize)
		if err != nil {
			return nil, 0, err
		}
		record.Key = kvBuf[:header.keySize]
		record.Value = kvBuf[header.keySize:]
	}

	// 记录总大小 = header + key + value
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
