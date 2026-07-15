package fio

import "os"

type FileIO struct {
	fd *os.File
}

func NewFileIO(path string) (*FileIO, error) {
	// O_APPEND 保证每次写入都是追加到文件末尾，避免覆盖已有数据
	fd, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_APPEND, DatafilePerm)
	if err != nil {
		return nil, err
	}
	return &FileIO{fd: fd}, nil
}
func (fio *FileIO) Read(b []byte, offset int64) (int, error) {
	return fio.fd.ReadAt(b, offset)
}
func (fio *FileIO) Write(b []byte) (int, error) {
	return fio.fd.Write(b)
}

func (fio *FileIO) Sync() error {
	return fio.fd.Sync()
}
func (fio *FileIO) Close() error {
	return fio.fd.Close()
}
