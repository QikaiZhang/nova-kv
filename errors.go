package novakv

import "errors"

var (
	// ErrKeyIsEmpty key 为空
	ErrKeyIsEmpty = errors.New("key is empty")

	// ErrKeyNotFound key 不存在
	ErrKeyNotFound = errors.New("key not found in database")

	// ErrDataFileNotFound 数据文件不存在
	ErrDataFileNotFound = errors.New("data file is not found")
)
