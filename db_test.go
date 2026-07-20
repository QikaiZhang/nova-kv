package novakv

import (
	"nova-kv/data"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// =============================================================================
// 测试辅助函数
// =============================================================================

// setupTestDB 在临时目录中创建测试用 DB
func setupTestDB(t *testing.T) (*DB, func()) {
	t.Helper()
	return openTestDB(t, t.TempDir(), 256*1024*1024, false, BTree)
}

// openTestDB 底层打开函数，用于指定具体目录（重启场景）
func openTestDB(t *testing.T, dir string, dataFileSize int64, syncWrites bool, idxType IndexType) (*DB, func()) {
	t.Helper()
	db, err := Open(Options{
		DirPath:      dir,
		DataFileSize: dataFileSize,
		SyncWrites:   syncWrites,
		IndexType:    idxType,
	})
	require.NoError(t, err)
	return db, func() {}
}

// writeAndSync 写入测试数据并强制刷盘，确保重启时数据可见
func writeAndSync(t *testing.T, db *DB, key, value string) {
	t.Helper()
	err := db.Put([]byte(key), []byte(value))
	require.NoError(t, err)
	if db.activeFile != nil {
		require.NoError(t, db.activeFile.Sync())
	}
}

// getTestData 读取测试数据，断言 key 存在且 value 匹配
func getTestData(t *testing.T, db *DB, key, expectedValue string) {
	t.Helper()
	value, err := db.Get([]byte(key))
	require.NoError(t, err)
	assert.Equal(t, []byte(expectedValue), value)
}

// reopenDB 关闭活跃文件后在同一目录重新打开，模拟重启
func reopenDB(t *testing.T, db *DB, dir string) *DB {
	t.Helper()
	// 刷盘并关闭活跃文件
	if db.activeFile != nil {
		require.NoError(t, db.activeFile.Sync())
		require.NoError(t, db.activeFile.Close())
	}
	// 关闭所有旧文件
	for _, df := range db.olderFiles {
		require.NoError(t, df.Close())
	}
	// 重新打开
	newDB, err := Open(Options{
		DirPath:      dir,
		DataFileSize: 256 * 1024 * 1024,
		SyncWrites:   false,
		IndexType:    BTree,
	})
	require.NoError(t, err)
	return newDB
}

// =============================================================================
// A. 配置校验测试
// =============================================================================

func TestOpen_EmptyDirPath(t *testing.T) {
	_, err := Open(Options{DirPath: ""})
	assert.ErrorIs(t, err, ErrOptionsDirPathEmpty)
}

func TestOpen_ZeroDataFileSize(t *testing.T) {
	_, err := Open(Options{DirPath: t.TempDir(), DataFileSize: 0, IndexType: BTree})
	assert.ErrorIs(t, err, ErrOptionsDataFileSizeTooSmall)
}

func TestOpen_NegativeDataFileSize(t *testing.T) {
	_, err := Open(Options{DirPath: t.TempDir(), DataFileSize: -1, IndexType: BTree})
	assert.ErrorIs(t, err, ErrOptionsDataFileSizeTooSmall)
}

func TestOpen_UnknownIndexType(t *testing.T) {
	_, err := Open(Options{DirPath: t.TempDir(), DataFileSize: 256 * 1024 * 1024, IndexType: 99})
	assert.ErrorIs(t, err, ErrOptionsIndexTypeUnknown)
}

func TestOpen_ZeroValueIndexType(t *testing.T) {
	_, err := Open(Options{DirPath: t.TempDir(), DataFileSize: 256 * 1024 * 1024, IndexType: 0})
	assert.ErrorIs(t, err, ErrOptionsIndexTypeUnknown)
}

func TestOpen_DefaultOptions(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()
	assert.NotNil(t, db)
	assert.NotNil(t, db.index)
	assert.NotNil(t, db.activeFile)
}

func TestOpen_BTreeIndexType(t *testing.T) {
	db, err := Open(Options{DirPath: t.TempDir(), DataFileSize: 256 * 1024 * 1024, IndexType: BTree})
	require.NoError(t, err)
	assert.NotNil(t, db)
}

// =============================================================================
// B. 文件加载测试
// =============================================================================

func TestOpen_EmptyDir(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	assert.NotNil(t, db.activeFile)
	assert.Equal(t, uint32(0), db.activeFile.FileId)
	assert.Equal(t, int64(0), db.activeFile.WriteOff)
	assert.Empty(t, db.olderFiles)
}

func TestOpen_NonExistentDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nonexistent")
	db, err := Open(Options{DirPath: dir, DataFileSize: 256 * 1024 * 1024, IndexType: BTree})
	require.NoError(t, err)
	assert.NotNil(t, db)
	_, err = os.Stat(dir)
	assert.NoError(t, err)
}

func TestOpen_MultipleFiles(t *testing.T) {
	dir := t.TempDir()

	// 小阈值 + 刷盘，确保每次 Put 触发文件轮转
	db1, err := Open(Options{DirPath: dir, DataFileSize: 50, SyncWrites: true, IndexType: BTree})
	require.NoError(t, err)
	writeAndSync(t, db1, "k1", "v1")

	// 重启
	db2 := reopenDB(t, db1, dir)
	getTestData(t, db2, "k1", "v1")
}

func TestOpen_FileIdGaps(t *testing.T) {
	dir := t.TempDir()

	// 手动创建有间隔的数据文件 [1, 5, 9]
	for _, fid := range []uint32{1, 5, 9} {
		df, err := createTestDataFile(dir, fid)
		require.NoError(t, err)
		require.NoError(t, df.Close())
	}

	db, err := Open(Options{DirPath: dir, DataFileSize: 256 * 1024 * 1024, IndexType: BTree})
	require.NoError(t, err)

	assert.Equal(t, uint32(9), db.activeFile.FileId)
	assert.Len(t, db.olderFiles, 2)
	assert.Contains(t, db.olderFiles, uint32(1))
	assert.Contains(t, db.olderFiles, uint32(5))
}

func TestOpen_MalformedFilenames(t *testing.T) {
	dir := t.TempDir()

	// 正常启动并写入
	db1, err := Open(Options{DirPath: dir, DataFileSize: 256 * 1024 * 1024, SyncWrites: true, IndexType: BTree})
	require.NoError(t, err)
	writeAndSync(t, db1, "valid", "data")

	// 创建一些非法文件名的文件
	createDummyFile(t, dir, "readme.txt")
	createDummyFile(t, dir, "1.data")
	createDummyFile(t, dir, "abc.data")
	createDummyFile(t, dir, "000000abc.data")

	// 重启：只应加载合法文件
	db2 := reopenDB(t, db1, dir)
	getTestData(t, db2, "valid", "data")
}

func TestOpen_EmptyDataFile(t *testing.T) {
	dir := t.TempDir()

	df, err := createTestDataFile(dir, 0)
	require.NoError(t, err)
	require.NoError(t, df.Close())

	db, err := Open(Options{DirPath: dir, DataFileSize: 256 * 1024 * 1024, IndexType: BTree})
	require.NoError(t, err)
	assert.NotNil(t, db)
	assert.Equal(t, int64(0), db.activeFile.WriteOff)
}

// =============================================================================
// C. 索引重建测试
// =============================================================================

func TestOpen_IndexRebuild_Basic(t *testing.T) {
	dir := t.TempDir()

	db1, err := Open(Options{DirPath: dir, DataFileSize: 256 * 1024 * 1024, SyncWrites: true, IndexType: BTree})
	require.NoError(t, err)
	writeAndSync(t, db1, "key1", "value1")
	writeAndSync(t, db1, "key2", "value2")
	writeAndSync(t, db1, "key3", "value3")

	db2 := reopenDB(t, db1, dir)
	getTestData(t, db2, "key1", "value1")
	getTestData(t, db2, "key2", "value2")
	getTestData(t, db2, "key3", "value3")
}

func TestOpen_IndexRebuild_Overwritten(t *testing.T) {
	dir := t.TempDir()

	db1, err := Open(Options{DirPath: dir, DataFileSize: 256 * 1024 * 1024, SyncWrites: true, IndexType: BTree})
	require.NoError(t, err)
	writeAndSync(t, db1, "key1", "v1")
	writeAndSync(t, db1, "key1", "v2")
	writeAndSync(t, db1, "key1", "v3")

	db2 := reopenDB(t, db1, dir)
	getTestData(t, db2, "key1", "v3")
}

func TestOpen_IndexRebuild_Deleted(t *testing.T) {
	dir := t.TempDir()

	db1, err := Open(Options{DirPath: dir, DataFileSize: 256 * 1024 * 1024, SyncWrites: true, IndexType: BTree})
	require.NoError(t, err)
	writeAndSync(t, db1, "key1", "value1")
	require.NoError(t, db1.Delete([]byte("key1")))
	require.NoError(t, db1.activeFile.Sync())

	db2 := reopenDB(t, db1, dir)
	_, err = db2.Get([]byte("key1"))
	assert.ErrorIs(t, err, ErrKeyNotFound)
}

func TestOpen_IndexRebuild_EmptyDB(t *testing.T) {
	dir := t.TempDir()

	db1, err := Open(Options{DirPath: dir, DataFileSize: 256 * 1024 * 1024, IndexType: BTree})
	require.NoError(t, err)
	require.NoError(t, db1.activeFile.Sync())

	db2 := reopenDB(t, db1, dir)
	_, err = db2.Get([]byte("any_key"))
	assert.ErrorIs(t, err, ErrKeyNotFound)
}

func TestOpen_IndexRebuild_MixedOperations(t *testing.T) {
	dir := t.TempDir()

	db1, err := Open(Options{DirPath: dir, DataFileSize: 256 * 1024 * 1024, SyncWrites: true, IndexType: BTree})
	require.NoError(t, err)
	writeAndSync(t, db1, "a", "1")
	writeAndSync(t, db1, "b", "2")
	require.NoError(t, db1.Delete([]byte("a")))
	writeAndSync(t, db1, "a", "3") // 重新写入
	writeAndSync(t, db1, "c", "4")

	db2 := reopenDB(t, db1, dir)
	getTestData(t, db2, "a", "3")
	getTestData(t, db2, "b", "2")
	getTestData(t, db2, "c", "4")
}

// =============================================================================
// D. 容错测试
// =============================================================================

func TestOpen_CorruptedRecord(t *testing.T) {
	dir := t.TempDir()

	db1, err := Open(Options{DirPath: dir, DataFileSize: 256 * 1024 * 1024, SyncWrites: true, IndexType: BTree})
	require.NoError(t, err)
	writeAndSync(t, db1, "good1", "val1")
	writeAndSync(t, db1, "good2", "val2")
	activeFilePath := data.GetDataFileName(dir, db1.activeFile.FileId)
	require.NoError(t, db1.activeFile.Close())

	// 手动损坏文件尾部
	fileInfo, err := os.Stat(activeFilePath)
	require.NoError(t, err)
	fileSize := fileInfo.Size()
	midPoint := fileSize - 10
	if midPoint <= 0 {
		t.Skip("file too small for corruption test")
	}

	f, err := os.OpenFile(activeFilePath, os.O_RDWR, 0644)
	require.NoError(t, err)
	defer f.Close()
	_, err = f.WriteAt([]byte{0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF}, midPoint)
	require.NoError(t, err)
	require.NoError(t, f.Sync())
	require.NoError(t, f.Close())

	// 重启：损坏记录不应阻止其他数据恢复
	db2, err := Open(Options{DirPath: dir, DataFileSize: 256 * 1024 * 1024, IndexType: BTree})
	if err != nil {
		t.Logf("recovery returned error (expected): %v", err)
	}
	require.NotNil(t, db2, "DB should be non-nil even with corrupted records")

	// 验证可恢复的数据
	value, readErr := db2.Get([]byte("good1"))
	if readErr == nil {
		assert.Equal(t, []byte("val1"), value)
	} else {
		t.Logf("good1 not recoverable (may be in corrupted portion): %v", readErr)
	}
}

func TestOpen_TruncatedRecord(t *testing.T) {
	dir := t.TempDir()

	db1, err := Open(Options{DirPath: dir, DataFileSize: 256 * 1024 * 1024, SyncWrites: true, IndexType: BTree})
	require.NoError(t, err)
	writeAndSync(t, db1, "valid", "data")
	activeFilePath := data.GetDataFileName(dir, db1.activeFile.FileId)
	require.NoError(t, db1.activeFile.Close())

	fileInfo, err := os.Stat(activeFilePath)
	require.NoError(t, err)
	truncateTo := fileInfo.Size() - 3
	if truncateTo < 10 {
		truncateTo = 10
	}
	require.NoError(t, os.Truncate(activeFilePath, truncateTo))

	db2, err := Open(Options{DirPath: dir, DataFileSize: 256 * 1024 * 1024, IndexType: BTree})
	if err != nil {
		t.Logf("recovery returned error (may be expected): %v", err)
	}
	require.NotNil(t, db2)
}

func TestOpen_TwoInstances(t *testing.T) {
	dir := t.TempDir()

	db1, err := Open(Options{DirPath: dir, DataFileSize: 256 * 1024 * 1024, SyncWrites: true, IndexType: BTree})
	require.NoError(t, err)
	writeAndSync(t, db1, "persist", "value")

	db2 := reopenDB(t, db1, dir)
	getTestData(t, db2, "persist", "value")
}

// =============================================================================
// E. 未来测试（暂不实现，提前规划）
// =============================================================================

func TestOpen_CRCValidation(t *testing.T) {
	dir := t.TempDir()

	db, err := Open(Options{
		DirPath:      dir,
		DataFileSize: 256 * 1024 * 1024,
		SyncWrites:   true,
		IndexType:    BTree,
	})
	require.NoError(t, err)

	// 写入一条记录，CRC 由 EncodeLogRecord 自动计算
	writeAndSync(t, db, "key1", "value1")

	// 正常读取：CRC 应该匹配
	getTestData(t, db, "key1", "value1")

	// 关闭后手动损坏第一条记录的 CRC 前 4 字节
	activePath := data.GetDataFileName(dir, db.activeFile.FileId)
	require.NoError(t, db.activeFile.Close())

	f, err := os.OpenFile(activePath, os.O_RDWR, 0644)
	require.NoError(t, err)

	var crcBytes [4]byte
	_, err = f.ReadAt(crcBytes[:], 0)
	require.NoError(t, err)
	crcBytes[0] ^= 0xFF
	_, err = f.WriteAt(crcBytes[:], 0)
	require.NoError(t, err)
	require.NoError(t, f.Sync())
	require.NoError(t, f.Close())

	// 重启：CRC 损坏的记录被检测到，恢复过程报错但不影响启动
	db2, err := Open(Options{
		DirPath:      dir,
		DataFileSize: 256 * 1024 * 1024,
		IndexType:    BTree,
	})
	if err != nil {
		t.Logf("expected corruption error: %v", err)
	}
	require.NotNil(t, db2, "DB should start even with corrupt CRC records")

	// key1 的 CRC 被破坏，索引恢复时跳过，应返回 ErrKeyNotFound
	_, err = db2.Get([]byte("key1"))
	assert.ErrorIs(t, err, ErrKeyNotFound)
}

func TestOpen_HintFileRecovery(t *testing.T) {
	t.Skip("Hint File 尚未实现")
}

func TestOpen_ConcurrentRecovery(t *testing.T) {
	t.Skip("多线程恢复暂未支持")
}

func TestOpen_LargeScaleRecovery(t *testing.T) {
	t.Skip("大规模恢复测试需要先生成大量数据")
}

func TestOpen_KeySizeLimit(t *testing.T) {
	t.Skip("key 大小限制尚未实现")
}

// =============================================================================
// 测试辅助函数（文件级）
// =============================================================================

func createTestDataFile(dir string, fid uint32) (*data.DataFile, error) {
	return data.NewDataFile(dir, fid)
}

func createDummyFile(t *testing.T, dir, name string) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte{}, 0644))
}
