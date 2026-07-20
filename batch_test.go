package novakv

import (
	"nova-kv/data"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// =============================================================================
// Close & Sync 测试
// =============================================================================

func TestDB_Close(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	err := db.Put([]byte("key1"), []byte("value1"))
	require.NoError(t, err)
	require.NoError(t, db.Sync())

	err = db.Close()
	assert.NoError(t, err)
}

func TestDB_Close_EmptyDB(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	err := db.Close()
	assert.NoError(t, err)
}

func TestDB_Close_Twice(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	require.NoError(t, db.Put([]byte("k"), []byte("v")))
	require.NoError(t, db.Close())

	// 第二次 Close 应失败（文件已关闭）
	err := db.Close()
	assert.Error(t, err)
}

func TestDB_Sync(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	err := db.Put([]byte("key1"), []byte("value1"))
	require.NoError(t, err)

	err = db.Sync()
	assert.NoError(t, err)

	// 数据应可在重启后恢复
	db2 := reopenDB(t, db, filepath.Dir(data.GetDataFileName(db.options.DirPath, db.activeFile.FileId)))
	defer db2.Close()
	getTestData(t, db2, "key1", "value1")
}

func TestDB_Sync_EmptyDB(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	err := db.Sync()
	assert.NoError(t, err)
}

func TestDB_Close_Simple(t *testing.T) {
	dir := t.TempDir()
	db, err := Open(Options{DirPath: dir, DataFileSize: 256 * 1024 * 1024, IndexType: BTree})
	require.NoError(t, err)

	require.NoError(t, db.Put([]byte("persist"), []byte("data")))
	require.NoError(t, db.Close())

	// 重启后验证
	db2, err := Open(Options{DirPath: dir, DataFileSize: 256 * 1024 * 1024, IndexType: BTree})
	require.NoError(t, err)
	defer db2.Close()

	getTestData(t, db2, "persist", "data")
}

// =============================================================================
// ListKeys 测试
// =============================================================================

func TestDB_ListKeys_Empty(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	keys := db.ListKeys()
	assert.Empty(t, keys)
}

func TestDB_ListKeys_Single(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	require.NoError(t, db.Put([]byte("key1"), []byte("value1")))
	keys := db.ListKeys()
	assert.Equal(t, 1, len(keys))
	assert.Equal(t, []byte("key1"), keys[0])
}

func TestDB_ListKeys_Multiple(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	keys := []string{"c", "a", "b", "d"}
	for _, k := range keys {
		require.NoError(t, db.Put([]byte(k), []byte("v"+k)))
	}

	result := db.ListKeys()
	assert.Equal(t, 4, len(result))

	// BTree 按序返回
	var keyStrs []string
	for _, k := range result {
		keyStrs = append(keyStrs, string(k))
	}
	assert.Equal(t, []string{"a", "b", "c", "d"}, keyStrs)
}

func TestDB_ListKeys_DeletedKey(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	require.NoError(t, db.Put([]byte("keep"), []byte("v1")))
	require.NoError(t, db.Put([]byte("remove"), []byte("v2")))
	require.NoError(t, db.Delete([]byte("remove")))

	keys := db.ListKeys()
	assert.Equal(t, 1, len(keys))
	assert.Equal(t, []byte("keep"), keys[0])
}

// =============================================================================
// Fold 测试
// =============================================================================

func TestDB_Fold_Empty(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	count := 0
	err := db.Fold(func(key []byte, value []byte) bool {
		count++
		return true
	})
	assert.NoError(t, err)
	assert.Equal(t, 0, count)
}

func TestDB_Fold_All(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	require.NoError(t, db.Put([]byte("a"), []byte("1")))
	require.NoError(t, db.Put([]byte("b"), []byte("2")))
	require.NoError(t, db.Put([]byte("c"), []byte("3")))

	var results []string
	err := db.Fold(func(key []byte, value []byte) bool {
		results = append(results, string(key)+"="+string(value))
		return true
	})
	assert.NoError(t, err)
	assert.Equal(t, []string{"a=1", "b=2", "c=3"}, results)
}

func TestDB_Fold_EarlyExit(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	for i := 0; i < 100; i++ {
		require.NoError(t, db.Put([]byte{byte(i)}, []byte{byte(i)}))
	}

	count := 0
	err := db.Fold(func(key []byte, value []byte) bool {
		count++
		return count < 10 // 遍历 9 个后停止
	})
	assert.NoError(t, err)
	assert.Equal(t, 10, count)
}

func TestDB_Fold_DeletedKeySkipped(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	require.NoError(t, db.Put([]byte("keep"), []byte("val")))
	require.NoError(t, db.Delete([]byte("keep")))

	// 墓碑应从索引中移除，Fold 不应遍历到
	count := 0
	err := db.Fold(func(key []byte, value []byte) bool {
		count++
		return true
	})
	assert.NoError(t, err)
	assert.Equal(t, 0, count)
}

func TestDB_Fold_SkipTxnUnfinished(t *testing.T) {
	dir := t.TempDir()

	// 写入一个未完成的 WriteBatch（不写 TxnFinished）
	db, err := Open(Options{DirPath: dir, DataFileSize: 256 * 1024 * 1024, SyncWrites: true, IndexType: BTree})
	require.NoError(t, err)

	wb := db.NewWriteBatch(DefaultWriteBatchOptions)
	require.NoError(t, wb.Put([]byte("txn-key"), []byte("txn-val")))
	require.NoError(t, wb.Commit())
	require.NoError(t, db.Close())

	// 重启后，已提交的 batch 数据应该可见
	db2, err := Open(Options{DirPath: dir, DataFileSize: 256 * 1024 * 1024, IndexType: BTree})
	require.NoError(t, err)
	defer db2.Close()

	found := false
	err = db2.Fold(func(key []byte, value []byte) bool {
		if string(key) == "txn-key" {
			found = true
			assert.Equal(t, []byte("txn-val"), value)
		}
		return true
	})
	assert.NoError(t, err)
	assert.True(t, found)
}

// =============================================================================
// WriteBatch 基本功能测试
// =============================================================================

func TestWriteBatch_Put(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	wb := db.NewWriteBatch(DefaultWriteBatchOptions)

	require.NoError(t, wb.Put([]byte("batch_key1"), []byte("batch_val1")))
	require.NoError(t, wb.Put([]byte("batch_key2"), []byte("batch_val2")))
	require.NoError(t, wb.Commit())

	getTestData(t, db, "batch_key1", "batch_val1")
	getTestData(t, db, "batch_key2", "batch_val2")
}

func TestWriteBatch_Delete(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	// 先直接写入
	require.NoError(t, db.Put([]byte("to_delete"), []byte("val")))

	// 用 batch 删除
	wb := db.NewWriteBatch(DefaultWriteBatchOptions)
	require.NoError(t, wb.Delete([]byte("to_delete")))
	require.NoError(t, wb.Commit())

	_, err := db.Get([]byte("to_delete"))
	assert.ErrorIs(t, err, ErrKeyNotFound)
}

func TestWriteBatch_Empty(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	wb := db.NewWriteBatch(DefaultWriteBatchOptions)
	// 空批次提交不应报错
	assert.NoError(t, wb.Commit())
}

func TestWriteBatch_EmptyKey(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	wb := db.NewWriteBatch(DefaultWriteBatchOptions)
	err := wb.Put([]byte{}, []byte("val"))
	assert.ErrorIs(t, err, ErrKeyIsEmpty)

	err = wb.Delete([]byte{})
	assert.ErrorIs(t, err, ErrKeyIsEmpty)
}

func TestWriteBatch_MaxBatchNum(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	opts := WriteBatchOptions{MaxBatchNum: 2}
	wb := db.NewWriteBatch(opts)

	require.NoError(t, wb.Put([]byte("k1"), []byte("v1")))
	require.NoError(t, wb.Put([]byte("k2"), []byte("v2")))

	// 第 3 条应失败
	err := wb.Put([]byte("k3"), []byte("v3"))
	assert.ErrorIs(t, err, ErrBatchFull)
}

func TestWriteBatch_SyncWrites(t *testing.T) {
	dir := t.TempDir()
	db, err := Open(Options{
		DirPath:      dir,
		DataFileSize: 256 * 1024 * 1024,
		IndexType:    BTree,
	})
	require.NoError(t, err)

	opts := WriteBatchOptions{SyncWrites: true}
	wb := db.NewWriteBatch(opts)
	require.NoError(t, wb.Put([]byte("sync_key"), []byte("sync_val")))
	require.NoError(t, wb.Commit())

	// 关闭后重新打开验证
	require.NoError(t, db.Close())

	db2, err := Open(Options{DirPath: dir, DataFileSize: 256 * 1024 * 1024, IndexType: BTree})
	require.NoError(t, err)
	defer db2.Close()

	getTestData(t, db2, "sync_key", "sync_val")
}

func TestWriteBatch_MixedOps(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	// 先写入一些已有数据
	require.NoError(t, db.Put([]byte("keep"), []byte("old_val")))
	require.NoError(t, db.Put([]byte("overwrite"), []byte("old")))

	wb := db.NewWriteBatch(DefaultWriteBatchOptions)
	require.NoError(t, wb.Put([]byte("new_key"), []byte("new_val")))        // 新增
	require.NoError(t, wb.Put([]byte("overwrite"), []byte("new_val")))      // 覆盖
	require.NoError(t, wb.Delete([]byte("keep")))                           // 删除
	require.NoError(t, wb.Commit())

	// 验证
	getTestData(t, db, "new_key", "new_val")
	getTestData(t, db, "overwrite", "new_val")
	_, err := db.Get([]byte("keep"))
	assert.ErrorIs(t, err, ErrKeyNotFound)
}

func TestWriteBatch_OverwriteInSameBatch(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	wb := db.NewWriteBatch(DefaultWriteBatchOptions)
	require.NoError(t, wb.Put([]byte("key"), []byte("first")))
	require.NoError(t, wb.Put([]byte("key"), []byte("second")))
	require.NoError(t, wb.Commit())

	// 后写入的覆盖先写入的
	getTestData(t, db, "key", "second")
}

func TestWriteBatch_SequentialSeqNo(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	// 验证 SeqNo 递增
	wb1 := db.NewWriteBatch(DefaultWriteBatchOptions)
	require.NoError(t, wb1.Put([]byte("k1"), []byte("v1")))
	require.NoError(t, wb1.Commit())

	wb2 := db.NewWriteBatch(DefaultWriteBatchOptions)
	require.NoError(t, wb2.Put([]byte("k2"), []byte("v2")))
	require.NoError(t, wb2.Commit())

	assert.Equal(t, uint64(3), db.nextSeqNo) // 从 1 开始，两次 commit 后是 3
}

// =============================================================================
// WriteBatch 崩溃恢复测试
// =============================================================================

func TestWriteBatch_Recovery_CommittedBatch(t *testing.T) {
	dir := t.TempDir()

	// 第一次：写入 batch 并提交
	db1, err := Open(Options{
		DirPath:      dir,
		DataFileSize: 256 * 1024 * 1024,
		SyncWrites:   true,
		IndexType:    BTree,
	})
	require.NoError(t, err)

	wb := db1.NewWriteBatch(WriteBatchOptions{SyncWrites: true})
	require.NoError(t, wb.Put([]byte("recovered_key"), []byte("recovered_val")))
	require.NoError(t, wb.Commit())
	require.NoError(t, db1.Close())

	// 重启：已提交的 batch 应恢复
	db2, err := Open(Options{
		DirPath:      dir,
		DataFileSize: 256 * 1024 * 1024,
		IndexType:    BTree,
	})
	require.NoError(t, err)
	defer db2.Close()

	getTestData(t, db2, "recovered_key", "recovered_val")
}

func TestWriteBatch_Recovery_MultipleBatches(t *testing.T) {
	dir := t.TempDir()

	db1, err := Open(Options{
		DirPath:      dir,
		DataFileSize: 256 * 1024 * 1024,
		SyncWrites:   true,
		IndexType:    BTree,
	})
	require.NoError(t, err)

	// Batch 1
	wb1 := db1.NewWriteBatch(WriteBatchOptions{SyncWrites: true})
	require.NoError(t, wb1.Put([]byte("b1_k1"), []byte("b1_v1")))
	require.NoError(t, wb1.Commit())

	// Batch 2
	wb2 := db1.NewWriteBatch(WriteBatchOptions{SyncWrites: true})
	require.NoError(t, wb2.Put([]byte("b2_k1"), []byte("b2_v1")))
	require.NoError(t, wb2.Put([]byte("b2_k2"), []byte("b2_v2")))
	require.NoError(t, wb2.Commit())

	// 直接 Put
	require.NoError(t, db1.Put([]byte("plain_key"), []byte("plain_val")))
	require.NoError(t, db1.Close())

	// 重启
	db2, err := Open(Options{
		DirPath:      dir,
		DataFileSize: 256 * 1024 * 1024,
		IndexType:    BTree,
	})
	require.NoError(t, err)
	defer db2.Close()

	getTestData(t, db2, "b1_k1", "b1_v1")
	getTestData(t, db2, "b2_k1", "b2_v1")
	getTestData(t, db2, "b2_k2", "b2_v2")
	getTestData(t, db2, "plain_key", "plain_val")
}

func TestWriteBatch_Recovery_IncompleteBatch(t *testing.T) {
	dir := t.TempDir()

	db1, err := Open(Options{
		DirPath:      dir,
		DataFileSize: 256 * 1024 * 1024,
		SyncWrites:   true,
		IndexType:    BTree,
	})
	require.NoError(t, err)

	// 先写入一个完整的 batch（正常提交）
	wb1 := db1.NewWriteBatch(WriteBatchOptions{SyncWrites: true})
	require.NoError(t, wb1.Put([]byte("good_key"), []byte("good_val")))
	require.NoError(t, wb1.Commit())

	// 手动模拟未完成的 batch：直接写入带 SeqNo 的记录，但不写 TxnFinished 标记
	// 需要先获取锁
	db1.mu.Lock()
	seqNo := db1.nextSeqNo
	db1.nextSeqNo++

	// 写入一条"脏"记录（属于未完成事务）
	dirtyRecord := &data.LogRecord{
		Key:   []byte("dirty_key"),
		Value: []byte("dirty_val"),
		Type:  data.LogRecordNormal,
		SeqNo: seqNo,
	}
	_, err = db1.appendLogRecordWithPos(dirtyRecord)
	db1.mu.Unlock()
	require.NoError(t, err)
	require.NoError(t, db1.activeFile.Sync())
	require.NoError(t, db1.Close())

	// 重启：脏数据不应出现在索引中
	db2, err := Open(Options{
		DirPath:      dir,
		DataFileSize: 256 * 1024 * 1024,
		IndexType:    BTree,
	})
	require.NoError(t, err)
	defer db2.Close()

	// 好的 batch 应该恢复
	getTestData(t, db2, "good_key", "good_val")

	// 脏数据不应存在
	_, err = db2.Get([]byte("dirty_key"))
	assert.ErrorIs(t, err, ErrKeyNotFound, "incomplete batch data should not be recovered")
}

func TestWriteBatch_Recovery_SeqNoContinuation(t *testing.T) {
	dir := t.TempDir()

	// 第一次：写入 3 个 batch
	db1, err := Open(Options{
		DirPath:      dir,
		DataFileSize: 256 * 1024 * 1024,
		SyncWrites:   true,
		IndexType:    BTree,
	})
	require.NoError(t, err)

	for i := 0; i < 3; i++ {
		wb := db1.NewWriteBatch(WriteBatchOptions{SyncWrites: true})
		require.NoError(t, wb.Put([]byte{byte('a' + i)}, []byte{byte('1' + i)}))
		require.NoError(t, wb.Commit())
	}
	require.NoError(t, db1.Close())

	// 重启后 nextSeqNo 应该正确恢复（3 个 batch → nextSeqNo = 4）
	db2, err := Open(Options{
		DirPath:      dir,
		DataFileSize: 256 * 1024 * 1024,
		IndexType:    BTree,
	})
	require.NoError(t, err)
	defer db2.Close()

	assert.Equal(t, uint64(4), db2.nextSeqNo,
		"nextSeqNo should be restored to max committed seqNo + 1")

	// 新 batch 应该继续使用恢复后的 SeqNo
	wb := db2.NewWriteBatch(DefaultWriteBatchOptions)
	require.NoError(t, wb.Put([]byte("after_recovery"), []byte("val")))
	require.NoError(t, wb.Commit())

	getTestData(t, db2, "after_recovery", "val")
	assert.Equal(t, uint64(5), db2.nextSeqNo)
}

func TestWriteBatch_Recovery_OnlyPlainPuts(t *testing.T) {
	dir := t.TempDir()

	db1, err := Open(Options{
		DirPath:      dir,
		DataFileSize: 256 * 1024 * 1024,
		SyncWrites:   true,
		IndexType:    BTree,
	})
	require.NoError(t, err)

	// 只有普通 Put，没有 WriteBatch
	require.NoError(t, db1.Put([]byte("k1"), []byte("v1")))
	require.NoError(t, db1.Put([]byte("k2"), []byte("v2")))
	require.NoError(t, db1.Close())

	db2, err := Open(Options{
		DirPath:      dir,
		DataFileSize: 256 * 1024 * 1024,
		IndexType:    BTree,
	})
	require.NoError(t, err)
	defer db2.Close()

	assert.Equal(t, uint64(1), db2.nextSeqNo, "no batches → nextSeqNo should stay at default 1")
	getTestData(t, db2, "k1", "v1")
	getTestData(t, db2, "k2", "v2")
}

// =============================================================================
// Multiple file rotation + WriteBatch recovery
// =============================================================================

func TestWriteBatch_Recovery_FileRotation(t *testing.T) {
	dir := t.TempDir()

	db1, err := Open(Options{
		DirPath:      dir,
		DataFileSize: 128, // 很小的文件，容易触发轮转
		SyncWrites:   true,
		IndexType:    BTree,
	})
	require.NoError(t, err)

	// 写多条数据，触发文件轮转
	for i := 0; i < 5; i++ {
		wb := db1.NewWriteBatch(WriteBatchOptions{SyncWrites: true})
		require.NoError(t, wb.Put([]byte{byte('a' + i)}, []byte{byte('A' + i)}))
		require.NoError(t, wb.Commit())
	}
	require.NoError(t, db1.Close())

	db2, err := Open(Options{
		DirPath:      dir,
		DataFileSize: 256 * 1024 * 1024,
		IndexType:    BTree,
	})
	require.NoError(t, err)
	defer db2.Close()

	for i := 0; i < 5; i++ {
		getTestData(t, db2, string([]byte{byte('a' + i)}), string([]byte{byte('A' + i)}))
	}
}

// =============================================================================
// 并发测试
// =============================================================================

func TestWriteBatch_ConcurrentBatches(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	done := make(chan bool, 10)
	for i := 0; i < 10; i++ {
		go func(id int) {
			wb := db.NewWriteBatch(DefaultWriteBatchOptions)
			key := []byte{byte('a' + id)}
			val := []byte{byte('A' + id)}
			_ = wb.Put(key, val)
			_ = wb.Commit()
			done <- true
		}(i)
	}

	for i := 0; i < 10; i++ {
		<-done
	}

	// 验证所有数据
	for i := 0; i < 10; i++ {
		getTestData(t, db, string([]byte{byte('a' + i)}), string([]byte{byte('A' + i)}))
	}
}

// =============================================================================
// 数据一致性测试
// =============================================================================

func TestWriteBatch_Consistency_WithPlainOps(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	// 先批量写
	wb := db.NewWriteBatch(DefaultWriteBatchOptions)
	for i := 0; i < 10; i++ {
		require.NoError(t, wb.Put([]byte{byte('a' + i)}, []byte{byte('A' + i)}))
	}
	require.NoError(t, wb.Commit())

	// 再用普通 Put 覆盖
	require.NoError(t, db.Put([]byte("a"), []byte("NEW_A")))

	// 再用普通 Delete 删除
	require.NoError(t, db.Delete([]byte("b")))

	// 再用 batch 操作
	wb2 := db.NewWriteBatch(DefaultWriteBatchOptions)
	require.NoError(t, wb2.Put([]byte("c"), []byte("NEW_C")))
	require.NoError(t, wb2.Delete([]byte("d")))
	require.NoError(t, wb2.Commit())

	// 验证
	getTestData(t, db, "a", "NEW_A")
	_, err := db.Get([]byte("b"))
	assert.ErrorIs(t, err, ErrKeyNotFound)
	getTestData(t, db, "c", "NEW_C")
	_, err = db.Get([]byte("d"))
	assert.ErrorIs(t, err, ErrKeyNotFound)

	// e-j 应该还是原始值
	for i := 4; i < 10; i++ {
		getTestData(t, db, string([]byte{byte('a' + i)}), string([]byte{byte('A' + i)}))
	}
}

// =============================================================================
// Simple helper for reopen without old FileIO close (tests have own reopenDB)
// =============================================================================

func TestDB_Close_AndReopen_PlainData(t *testing.T) {
	dir := t.TempDir()

	db1, err := Open(Options{
		DirPath:      dir,
		DataFileSize: 256 * 1024 * 1024,
		SyncWrites:   true,
		IndexType:    BTree,
	})
	require.NoError(t, err)

	require.NoError(t, db1.Put([]byte("hello"), []byte("world")))
	require.NoError(t, db1.Close())

	db2, err := Open(Options{
		DirPath:      dir,
		DataFileSize: 256 * 1024 * 1024,
		IndexType:    BTree,
	})
	require.NoError(t, err)
	defer db2.Close()

	getTestData(t, db2, "hello", "world")
}

func TestDB_Close_AndReopen_BatchData(t *testing.T) {
	dir := t.TempDir()

	db1, err := Open(Options{
		DirPath:      dir,
		DataFileSize: 256 * 1024 * 1024,
		SyncWrites:   true,
		IndexType:    BTree,
	})
	require.NoError(t, err)

	wb := db1.NewWriteBatch(WriteBatchOptions{SyncWrites: true})
	require.NoError(t, wb.Put([]byte("batch_hello"), []byte("batch_world")))
	require.NoError(t, wb.Commit())
	require.NoError(t, db1.Close())

	db2, err := Open(Options{
		DirPath:      dir,
		DataFileSize: 256 * 1024 * 1024,
		IndexType:    BTree,
	})
	require.NoError(t, err)
	defer db2.Close()

	getTestData(t, db2, "batch_hello", "batch_world")
}
