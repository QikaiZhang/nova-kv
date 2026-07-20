package novakv

import (
	"fmt"
	"nova-kv/data"
	"sync"
)

// =============================================================================
// WriteBatch —— 事务批量写入
// =============================================================================
//
// 设计思路（强烈建议手写理解）：
//
// WriteBatch 提供原子批量写入能力。用户将多个 Put/Delete 操作暂存到 pendingWrites 中，
// 调用 Commit 时一次性写入磁盘并更新索引。
//
// 事务保证：
//  1. 每个 WriteBatch 分配一个全局递增的 SeqNo
//  2. 所有 pending 记录的 LogRecord.SeqNo 设为该 SeqNo
//  3. 批量写入磁盘后，追加一条 LogRecordTxnFinished 标记
//  4. 遇到 TxnFinished 标记才更新索引，保证原子性
//
// 崩溃恢复：
//   - 重启时遍历数据文件，SeqNo>0 的记录暂存
//   - 遇到 TxnFinished 标记 → 该 SeqNo 的 batch 已完整提交，更新索引
//   - 未遇到 TxnFinished 标记 → 未完成的 batch，丢弃（merge 时清理）
//
// 当前使用全局锁保证串行化，后续可升级为简化 MVCC。

// WriteBatchOptions WriteBatch 配置项
type WriteBatchOptions struct {
	// MaxBatchNum 一个批次中最大的数据量，0 表示不限制
	MaxBatchNum int

	// SyncWrites 每一次 Commit 提交时是否持久化
	SyncWrites bool
}

// DefaultWriteBatchOptions 默认 WriteBatch 配置
var DefaultWriteBatchOptions = WriteBatchOptions{
	MaxBatchNum: 0,     // 不限制
	SyncWrites:  false, // 默认不强制刷盘
}

// WriteBatch 批量写入事务
type WriteBatch struct {
	mu            *sync.RWMutex
	db            *DB
	opts          WriteBatchOptions
	pendingWrites map[string]*data.LogRecord // key -> 待写入的记录
}

// NewWriteBatch 创建一个 WriteBatch
func (db *DB) NewWriteBatch(opts WriteBatchOptions) *WriteBatch {
	return &WriteBatch{
		mu:            new(sync.RWMutex),
		db:            db,
		opts:          opts,
		pendingWrites: make(map[string]*data.LogRecord),
	}
}

// Put 向 WriteBatch 中添加一个写入操作
func (wb *WriteBatch) Put(key []byte, value []byte) error {
	if len(key) == 0 {
		return ErrKeyIsEmpty
	}

	// 检查是否超过最大批次数量
	if wb.opts.MaxBatchNum > 0 && len(wb.pendingWrites) >= wb.opts.MaxBatchNum {
		return ErrBatchFull
	}

	wb.mu.Lock()
	defer wb.mu.Unlock()

	wb.pendingWrites[string(key)] = &data.LogRecord{
		Key:   key,
		Value: value,
		Type:  data.LogRecordNormal,
	}
	return nil
}

// Delete 向 WriteBatch 中添加一个删除操作
func (wb *WriteBatch) Delete(key []byte) error {
	if len(key) == 0 {
		return ErrKeyIsEmpty
	}

	if wb.opts.MaxBatchNum > 0 && len(wb.pendingWrites) >= wb.opts.MaxBatchNum {
		return ErrBatchFull
	}

	wb.mu.Lock()
	defer wb.mu.Unlock()

	wb.pendingWrites[string(key)] = &data.LogRecord{
		Key:  key,
		Type: data.LogRecordDeleted,
	}
	return nil
}

// Strongly Recommended to Handwrite
// Commit 提交 WriteBatch 中的所有操作
//
// 核心流程：
//  1. 获取全局锁，分配 SeqNo
//  2. 遍历 pendingWrites，为每条记录设置 SeqNo，写入磁盘
//  3. 写入 TxnFinished 标记（同一 SeqNo）
//  4. 根据需要 Sync
//  5. 更新内存索引
//  6. 清空 pendingWrites
//
// 原子性保证：索引只在遇到 TxnFinished 标记后才更新
func (wb *WriteBatch) Commit() error {
	wb.mu.Lock()
	defer wb.mu.Unlock()

	// 空批次直接返回
	if len(wb.pendingWrites) == 0 {
		return nil
	}

	// 1. 获取 DB 全局锁，分配 SeqNo
	wb.db.mu.Lock()
	defer wb.db.mu.Unlock()

	seqNo := wb.db.nextSeqNo
	wb.db.nextSeqNo++

	// 2. 暂存每条记录写入的位置，用于后续更新索引
	type position struct {
		key      []byte
		pos      *data.LogRecordPos
		recType  data.LogRecordType
	}
	positions := make([]position, 0, len(wb.pendingWrites))

	for _, rec := range wb.pendingWrites {
		// 设置事务 SeqNo
		rec.SeqNo = seqNo

		// 写入磁盘
		pos, err := wb.db.appendLogRecordWithPos(rec)
		if err != nil {
			return fmt.Errorf("write batch commit: append record: %w", err)
		}

		positions = append(positions, position{
			key:     rec.Key,
			pos:     pos,
			recType: rec.Type,
		})
	}

	// 3. 写入事务完成标记
	finishRecord := &data.LogRecord{
		Type:  data.LogRecordTxnFinished,
		SeqNo: seqNo,
	}
	if _, err := wb.db.appendLogRecordWithPos(finishRecord); err != nil {
		return fmt.Errorf("write batch commit: append txn finish: %w", err)
	}

	// 4. 根据配置决定是否刷盘
	if wb.opts.SyncWrites {
		if err := wb.db.activeFile.Sync(); err != nil {
			return fmt.Errorf("write batch commit: sync: %w", err)
		}
	}

	// 5. 更新内存索引（此时磁盘上 TxnFinished 标记已写入，即使崩溃也可恢复）
	for _, p := range positions {
		if p.recType == data.LogRecordNormal {
			wb.db.index.Put(p.key, p.pos)
		} else if p.recType == data.LogRecordDeleted {
			wb.db.index.Delete(p.key)
		}
	}

	// 6. 清空 pendingWrites
	wb.pendingWrites = make(map[string]*data.LogRecord)

	return nil
}
