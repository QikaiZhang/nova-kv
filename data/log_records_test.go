package data

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// =============================================================================
// LogRecord 编解码测试
// =============================================================================

func TestEncodeLogRecord_Normal(t *testing.T) {
	rec := &LogRecord{
		Key:   []byte("hello"),
		Value: []byte("world"),
		Type:  LogRecordNormal,
		SeqNo: 0,
	}
	encoded, size := EncodeLogRecord(rec)
	assert.NotNil(t, encoded)
	assert.Greater(t, size, int64(0))

	// 解码验证
	header, headerSize, err := DecodeLogRecordHeader(encoded)
	require.NoError(t, err)
	assert.NotNil(t, header)
	assert.Equal(t, LogRecordNormal, header.recordType)
	assert.Equal(t, uint32(5), header.keySize)
	assert.Equal(t, uint32(5), header.valueSize)
	assert.Equal(t, uint64(0), header.seqNo)

	// key+value 应在正确位置
	kvBuf := encoded[headerSize:]
	assert.Equal(t, []byte("hello"), kvBuf[:5])
	assert.Equal(t, []byte("world"), kvBuf[5:])
}

func TestEncodeLogRecord_Deleted(t *testing.T) {
	rec := &LogRecord{
		Key:  []byte("deleteme"),
		Type: LogRecordDeleted,
	}
	encoded, _ := EncodeLogRecord(rec)

	header, _, err := DecodeLogRecordHeader(encoded)
	require.NoError(t, err)
	assert.Equal(t, LogRecordDeleted, header.recordType)
	assert.Equal(t, uint32(8), header.keySize)
	assert.Equal(t, uint32(0), header.valueSize)
}

func TestEncodeLogRecord_WithSeqNo(t *testing.T) {
	rec := &LogRecord{
		Key:   []byte("txn-key"),
		Value: []byte("txn-value"),
		Type:  LogRecordNormal,
		SeqNo: 42,
	}
	encoded, _ := EncodeLogRecord(rec)

	header, _, err := DecodeLogRecordHeader(encoded)
	require.NoError(t, err)
	assert.Equal(t, uint64(42), header.seqNo)
	assert.Equal(t, uint32(7), header.keySize)
	assert.Equal(t, uint32(9), header.valueSize)
}

func TestEncodeLogRecord_TxnFinished(t *testing.T) {
	rec := &LogRecord{
		Type:  LogRecordTxnFinished,
		SeqNo: 7,
	}
	encoded, _ := EncodeLogRecord(rec)

	header, _, err := DecodeLogRecordHeader(encoded)
	require.NoError(t, err)
	assert.Equal(t, LogRecordTxnFinished, header.recordType)
	assert.Equal(t, uint64(7), header.seqNo)
	assert.Equal(t, uint32(0), header.keySize)
	assert.Equal(t, uint32(0), header.valueSize)
}

func TestEncodeLogRecord_LargeSeqNo(t *testing.T) {
	rec := &LogRecord{
		Key:   []byte("k"),
		Value: []byte("v"),
		Type:  LogRecordNormal,
		SeqNo: 1<<63 - 1, // max int64
	}
	encoded, _ := EncodeLogRecord(rec)

	header, _, err := DecodeLogRecordHeader(encoded)
	require.NoError(t, err)
	assert.Equal(t, uint64(1<<63-1), header.seqNo)
}

func TestEncodeLogRecord_EmptyKey(t *testing.T) {
	rec := &LogRecord{
		Key:   []byte{},
		Value: []byte("val"),
		Type:  LogRecordNormal,
	}
	encoded, _ := EncodeLogRecord(rec)

	header, _, err := DecodeLogRecordHeader(encoded)
	require.NoError(t, err)
	assert.Equal(t, uint32(0), header.keySize)
	assert.Equal(t, uint32(3), header.valueSize)
}

func TestEncodeLogRecord_EmptyValue(t *testing.T) {
	rec := &LogRecord{
		Key:   []byte("key"),
		Value: []byte{},
		Type:  LogRecordNormal,
	}
	encoded, _ := EncodeLogRecord(rec)

	header, _, err := DecodeLogRecordHeader(encoded)
	require.NoError(t, err)
	assert.Equal(t, uint32(3), header.keySize)
	assert.Equal(t, uint32(0), header.valueSize)
}

func TestEncodeLogRecord_NilKeyValue(t *testing.T) {
	rec := &LogRecord{
		Key:   nil,
		Value: nil,
		Type:  LogRecordNormal,
	}
	encoded, _ := EncodeLogRecord(rec)

	header, _, err := DecodeLogRecordHeader(encoded)
	require.NoError(t, err)
	assert.Equal(t, uint32(0), header.keySize)
	assert.Equal(t, uint32(0), header.valueSize)
}

func TestDecodeLogRecordHeader_CRC(t *testing.T) {
	rec := &LogRecord{
		Key:   []byte("test"),
		Value: []byte("data"),
		Type:  LogRecordNormal,
	}
	encoded, _ := EncodeLogRecord(rec)

	// CRC 前 4 字节应该非零
	crc := encoded[:4]
	assert.NotEqual(t, []byte{0, 0, 0, 0}, crc)

	header, _, err := DecodeLogRecordHeader(encoded)
	require.NoError(t, err)

	// 从编码数据中读 CRC（LE）
	storedCRC := uint32(encoded[0]) | uint32(encoded[1])<<8 | uint32(encoded[2])<<16 | uint32(encoded[3])<<24
	assert.Equal(t, storedCRC, header.crc)

	// DecodeLogRecordHeader 读的是 header 中存储的 CRC（buf[:4]）
	// 修改 header 中的 CRC 字段，header.crc 应随之变化
	encodedCopy := make([]byte, len(encoded))
	copy(encodedCopy, encoded)
	encodedCopy[0] ^= 0xFF // 翻转 CRC 的第一个字节

	header2, _, err := DecodeLogRecordHeader(encodedCopy)
	require.NoError(t, err)
	assert.NotEqual(t, header.crc, header2.crc)
}

func TestDecodeLogRecordHeader_TooShort(t *testing.T) {
	_, _, err := DecodeLogRecordHeader([]byte{1, 2, 3})
	assert.Error(t, err)

	_, _, err = DecodeLogRecordHeader(nil)
	assert.Error(t, err)
}

func TestDecodeLogRecordHeader_TruncatedKeySize(t *testing.T) {
	// 构造一个 valid header 后截断 KeySize 的 varint
	rec := &LogRecord{
		Key:   []byte("hello"),
		Value: []byte("world"),
		Type:  LogRecordNormal,
	}
	encoded, _ := EncodeLogRecord(rec)

	// 从第 5 字节（Type 后）开始截断
	if len(encoded) > 6 {
		truncated := encoded[:6]
		_, _, err := DecodeLogRecordHeader(truncated)
		assert.Error(t, err)
	}
}

func TestDecodeLogRecordHeader_TruncatedValueSize(t *testing.T) {
	rec := &LogRecord{
		Key:   []byte("hello"),
		Value: []byte("world"),
		Type:  LogRecordNormal,
	}
	encoded, _ := EncodeLogRecord(rec)

	// 截断在 Type 和 KeySize 之间（仅 5 字节），必然失败
	truncated := encoded[:5]
	_, _, err := DecodeLogRecordHeader(truncated)
	assert.Error(t, err)
}

func TestEncodeLogRecord_RoundTrip_WithSeqNo(t *testing.T) {
	testCases := []struct {
		name  string
		rec   LogRecord
	}{
		{"zero seq", LogRecord{Key: []byte("k"), Value: []byte("v"), Type: LogRecordNormal, SeqNo: 0}},
		{"small seq", LogRecord{Key: []byte("k"), Value: []byte("v"), Type: LogRecordNormal, SeqNo: 1}},
		{"medium seq", LogRecord{Key: []byte("k"), Value: []byte("v"), Type: LogRecordNormal, SeqNo: 1000}},
		{"large seq", LogRecord{Key: []byte("k"), Value: []byte("v"), Type: LogRecordNormal, SeqNo: 1 << 40}},
		{"deleted", LogRecord{Key: []byte("del"), Type: LogRecordDeleted, SeqNo: 5}},
		{"txn finish", LogRecord{Type: LogRecordTxnFinished, SeqNo: 99}},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			encoded, _ := EncodeLogRecord(&tc.rec)
			header, _, err := DecodeLogRecordHeader(encoded)
			require.NoError(t, err)

			assert.Equal(t, tc.rec.Type, header.recordType)
			assert.Equal(t, uint32(len(tc.rec.Key)), header.keySize)
			assert.Equal(t, uint32(len(tc.rec.Value)), header.valueSize)
			assert.Equal(t, tc.rec.SeqNo, header.seqNo)
		})
	}
}

// binaryLittleEndian 辅助函数：从 4 字节中读取 uint32（LE）

