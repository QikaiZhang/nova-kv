package fio

import (
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// 测试初始化
func TestNewFileIO(t *testing.T) {
	// 创建临时目录
	tempDir := t.TempDir()
	filePath := filepath.Join(tempDir, "test.data")

	// 测试正常创建
	fio, err := NewFileIO(filePath)
	require.NoError(t, err)
	assert.NotNil(t, fio)
	assert.NotNil(t, fio.fd)

	// 验证文件是否存在
	_, err = os.Stat(filePath)
	assert.NoError(t, err)

	// 测试关闭
	err = fio.Close()
	assert.NoError(t, err)

	// 测试文件已存在的情况
	fio2, err := NewFileIO(filePath)
	require.NoError(t, err)
	assert.NotNil(t, fio2)
	err = fio2.Close()
	assert.NoError(t, err)
}

// 测试写操作 - 对应 Write([]byte) (int, error)
func TestFileIO_Write(t *testing.T) {
	tempDir := t.TempDir()
	filePath := filepath.Join(tempDir, "test.data")
	fio, err := NewFileIO(filePath)
	require.NoError(t, err)
	defer fio.Close()

	tests := []struct {
		name    string
		data    []byte
		wantN   int
		wantErr error
	}{
		{
			name:    "写入正常数据",
			data:    []byte("hello world"),
			wantN:   11,
			wantErr: nil,
		},
		{
			name:    "写入空数据",
			data:    []byte{},
			wantN:   0,
			wantErr: nil,
		},
		{
			name:    "写入二进制数据",
			data:    []byte{0x00, 0x01, 0x02, 0xFF},
			wantN:   4,
			wantErr: nil,
		},
		{
			name:    "写入中文字符",
			data:    []byte("你好世界"),
			wantN:   12, // UTF-8 编码，每个中文3字节
			wantErr: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			n, err := fio.Write(tt.data)
			assert.Equal(t, tt.wantN, n)
			assert.Equal(t, tt.wantErr, err)
		})
	}
}

// 测试读操作 - 对应 Read([]byte, int64) (int, error)
func TestFileIO_Read(t *testing.T) {
	tempDir := t.TempDir()
	filePath := filepath.Join(tempDir, "test.data")
	fio, err := NewFileIO(filePath)
	require.NoError(t, err)
	defer fio.Close()

	// 准备测试数据 - 使用 Write 写入
	testData := []byte("hello world this is a test")
	_, err = fio.Write(testData)
	require.NoError(t, err)

	tests := []struct {
		name      string
		offset    int64
		size      int
		expected  []byte
		expectErr bool
	}{
		{
			name:      "从开头读取",
			offset:    0,
			size:      5,
			expected:  []byte("hello"),
			expectErr: false,
		},
		{
			name:      "从中间读取",
			offset:    6,
			size:      5,
			expected:  []byte("world"),
			expectErr: false,
		},
		{
			name:      "读取到文件末尾",
			offset:    0,
			size:      len(testData),
			expected:  testData,
			expectErr: false,
		},
		{
			name:      "读取超出文件大小",
			offset:    0,
			size:      len(testData) + 10,
			expected:  testData,
			expectErr: true, // ReadAt 在超出文件末尾时返回 io.EOF
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			buf := make([]byte, tt.size)
			n, err := fio.Read(buf, tt.offset)

			if tt.expectErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				// 只比较实际读取到的部分
				if n < len(tt.expected) {
					assert.Equal(t, tt.expected[:n], buf[:n])
				} else {
					assert.Equal(t, tt.expected, buf[:n])
				}
			}
		})
	}
}

// 测试顺序写入 - 验证 Write 的追加效果
func TestFileIO_SequentialWrite(t *testing.T) {
	tempDir := t.TempDir()
	filePath := filepath.Join(tempDir, "test.data")
	fio, err := NewFileIO(filePath)
	require.NoError(t, err)
	defer fio.Close()

	// 第一次写入
	n1, err := fio.Write([]byte("first"))
	require.NoError(t, err)
	assert.Equal(t, 5, n1)

	// 第二次写入（追加）
	n2, err := fio.Write([]byte("second"))
	require.NoError(t, err)
	assert.Equal(t, 6, n2)

	// 读取整个文件验证
	fileInfo, err := fio.fd.Stat()
	require.NoError(t, err)
	fileSize := fileInfo.Size()
	assert.Equal(t, int64(11), fileSize) // 5 + 6 = 11

	// 从开头读取全部内容
	buf := make([]byte, fileSize)
	n, err := fio.Read(buf, 0)
	assert.NoError(t, err)
	assert.Equal(t, int(fileSize), n)
	assert.Equal(t, "firstsecond", string(buf))
}

// 测试读写结合 - 随机访问
// 测试同步功能
func TestFileIO_Sync(t *testing.T) {
	tempDir := t.TempDir()
	filePath := filepath.Join(tempDir, "test.data")
	fio, err := NewFileIO(filePath)
	require.NoError(t, err)
	defer fio.Close()

	// 写入数据
	_, err = fio.Write([]byte("test data"))
	require.NoError(t, err)

	// 同步
	err = fio.Sync()
	assert.NoError(t, err)

	// 多次同步
	err = fio.Sync()
	assert.NoError(t, err)

	// 空文件同步
	fio2, err := NewFileIO(filepath.Join(tempDir, "empty.data"))
	require.NoError(t, err)
	defer fio2.Close()
	err = fio2.Sync()
	assert.NoError(t, err)
}

// 测试关闭功能
func TestFileIO_Close(t *testing.T) {
	tempDir := t.TempDir()
	filePath := filepath.Join(tempDir, "test.data")
	fio, err := NewFileIO(filePath)
	require.NoError(t, err)

	// 写入数据
	_, err = fio.Write([]byte("test data"))
	require.NoError(t, err)

	// 关闭
	err = fio.Close()
	assert.NoError(t, err)

	// 再次关闭（应该返回错误）
	err = fio.Close()
	assert.Error(t, err)

	// 关闭后尝试读取（应该返回错误）
	buf := make([]byte, 10)
	_, err = fio.Read(buf, 0)
	assert.Error(t, err)

	// 关闭后尝试写入（应该返回错误）
	_, err = fio.Write([]byte("test"))
	assert.Error(t, err)

	// 关闭后尝试同步（应该返回错误）
	err = fio.Sync()
	assert.Error(t, err)
}

// 测试并发写入
func TestFileIO_ConcurrentWrite(t *testing.T) {
	tempDir := t.TempDir()
	filePath := filepath.Join(tempDir, "test.data")
	fio, err := NewFileIO(filePath)
	require.NoError(t, err)
	defer fio.Close()

	var wg sync.WaitGroup
	numGoroutines := 10

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			data := []byte{byte(id)}
			n, err := fio.Write(data)
			assert.NoError(t, err)
			assert.Equal(t, 1, n)
		}(i)
	}
	wg.Wait()

	// 验证文件大小应该等于 goroutines 数量
	fileInfo, err := fio.fd.Stat()
	require.NoError(t, err)
	assert.Equal(t, int64(numGoroutines), fileInfo.Size())

	// 验证每个字节都被写入
	buf := make([]byte, numGoroutines)
	_, err = fio.Read(buf, 0)
	require.NoError(t, err)

	// 检查是否包含所有值
	valueMap := make(map[byte]bool)
	for _, b := range buf {
		valueMap[b] = true
	}
	// 注意：由于并发写入不保证顺序，但应该包含所有值
	for i := 0; i < numGoroutines; i++ {
		assert.True(t, valueMap[byte(i)], "missing value: %d", i)
	}
}

// 测试大文件操作
func TestFileIO_LargeFile(t *testing.T) {
	tempDir := t.TempDir()
	filePath := filepath.Join(tempDir, "large.data")
	fio, err := NewFileIO(filePath)
	require.NoError(t, err)
	defer fio.Close()

	// 写入大量数据（1MB）
	largeData := make([]byte, 1024*1024)
	for i := range largeData {
		largeData[i] = byte(i % 256)
	}

	// 分块写入
	chunkSize := 64 * 1024 // 64KB
	for i := 0; i < len(largeData); i += chunkSize {
		end := i + chunkSize
		if end > len(largeData) {
			end = len(largeData)
		}
		_, err := fio.Write(largeData[i:end])
		require.NoError(t, err)
	}

	// 验证文件大小
	fileInfo, err := fio.fd.Stat()
	require.NoError(t, err)
	assert.Equal(t, int64(len(largeData)), fileInfo.Size())

	// 读取并验证部分数据（从不同偏移量读取）
	testOffsets := []int64{0, 100, 1000, 10000, 100000, 500000}
	for _, offset := range testOffsets {
		buf := make([]byte, 100)
		n, err := fio.Read(buf, offset)
		assert.NoError(t, err)
		assert.Equal(t, 100, n)

		// 验证数据正确性
		for i := 0; i < n; i++ {
			expected := byte((int(offset) + i) % 256)
			assert.Equal(t, expected, buf[i], "offset: %d, index: %d", offset, i)
		}
	}
}

// 测试错误处理
func TestFileIO_Errors(t *testing.T) {
	tempDir := t.TempDir()
	filePath := filepath.Join(tempDir, "test.data")
	fio, err := NewFileIO(filePath)
	require.NoError(t, err)
	defer fio.Close()

	// 测试无效路径
	_, err = NewFileIO("/invalid/path/file.data")
	assert.Error(t, err)

	// 测试负偏移量读取
	buf := make([]byte, 10)
	_, err = fio.Read(buf, -1)
	assert.Error(t, err)

	// 测试读取 nil buffer - Go 的 os.File.ReadAt 不对此报错
	// 此处仅验证不会 panic
	assert.NotPanics(t, func() {
		fio.Read(nil, 0)
	})

	// 测试写入 nil
	_, err = fio.Write(nil)
	assert.NoError(t, err) // Write nil 是允许的

	// 测试在关闭的文件上操作
	fio.Close()
	_, err = fio.Write([]byte("test"))
	assert.Error(t, err)
	_, err = fio.Read(make([]byte, 10), 0)
	assert.Error(t, err)
	err = fio.Sync()
	assert.Error(t, err)
}

// 基准测试 - 写入性能
func BenchmarkFileIO_Write(b *testing.B) {
	tempDir := b.TempDir()
	filePath := filepath.Join(tempDir, "bench.data")
	fio, err := NewFileIO(filePath)
	require.NoError(b, err)
	defer fio.Close()

	data := []byte("benchmark test data")
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_, err := fio.Write(data)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// 基准测试 - 读取性能
func BenchmarkFileIO_Read(b *testing.B) {
	tempDir := b.TempDir()
	filePath := filepath.Join(tempDir, "bench.data")
	fio, err := NewFileIO(filePath)
	require.NoError(b, err)
	defer fio.Close()

	// 准备测试数据
	data := []byte("benchmark test data")
	for i := 0; i < 10000; i++ {
		_, err := fio.Write(data)
		if err != nil {
			b.Fatal(err)
		}
	}

	buf := make([]byte, len(data))
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		offset := int64((i % 10000) * len(data))
		_, err := fio.Read(buf, offset)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// 基准测试 - 大文件写入
func BenchmarkFileIO_LargeWrite(b *testing.B) {
	tempDir := b.TempDir()
	filePath := filepath.Join(tempDir, "bench-large.data")
	fio, err := NewFileIO(filePath)
	require.NoError(b, err)
	defer fio.Close()

	data := make([]byte, 1024*1024) // 1MB
	for i := range data {
		data[i] = byte(i % 256)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := fio.Write(data)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func TestFileIO_Size(t *testing.T) {
	tempDir := t.TempDir()
	filePath := filepath.Join(tempDir, "test.data")
	fio, err := NewFileIO(filePath)
	require.NoError(t, err)
	defer fio.Close()

	// 空文件 Size 应为 0
	size, err := fio.Size()
	require.NoError(t, err)
	assert.Equal(t, int64(0), size)

	// 写入后 Size 应更新
	written := []byte("hello")
	_, err = fio.Write(written)
	require.NoError(t, err)
	size, err = fio.Size()
	require.NoError(t, err)
	assert.Equal(t, int64(len(written)), size)

	// 关闭后 Size 应该报错
	require.NoError(t, fio.Close())
	_, err = fio.Size()
	assert.Error(t, err)
}
