package novakv

import (
	"errors"
	"fmt"
)

var (
	// ErrKeyIsEmpty key 为空
	ErrKeyIsEmpty = errors.New("key is empty")

	// ErrKeyNotFound key 不存在
	ErrKeyNotFound = errors.New("key not found in database")

	// ErrDataFileNotFound 数据文件不存在
	ErrDataFileNotFound = errors.New("data file is not found")

	// ErrOptionsDirPathEmpty 配置项：数据目录不能为空
	ErrOptionsDirPathEmpty = errors.New("database dir path cannot be empty")

	// ErrOptionsDataFileSizeTooSmall 配置项：数据文件大小过小
	ErrOptionsDataFileSizeTooSmall = errors.New("data file size too small")

	// ErrOptionsIndexTypeUnknown 配置项：未知索引类型
	ErrOptionsIndexTypeUnknown = errors.New("unknown index type")
)

// CorruptedRecordError 损坏记录错误，携带文件 ID 和偏移信息
// 调用方可用 errors.As 提取上下文
type CorruptedRecordError struct {
	FileId uint32
	Offset int64
	Cause  error
}

func (e *CorruptedRecordError) Error() string {
	return fmt.Sprintf("corrupted log record in file %09d.data at offset %d: %v", e.FileId, e.Offset, e.Cause)
}

func (e *CorruptedRecordError) Unwrap() error {
	return e.Cause
}
