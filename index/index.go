package index

import (
	"bytes"
	"nova-kv/data"

	"github.com/google/btree"
)

// Indexer 内存索引接口
type Indexer interface {
	// Put 向索引中存储 key 对应的数据位置信息
	Put(key []byte, pos *data.LogRecordPos) bool
	// Get 根据 key 取出数据的位置信息
	Get(key []byte) *data.LogRecordPos
	// Delete 根据 key 删除索引
	Delete(key []byte) bool
	// NewIterator 创建索引迭代器
	NewIterator(opts IteratorOptions) Iterator
	// Size 返回索引中 key 的数量
	Size() int
}

// Iterator 索引迭代器接口
// 用于遍历索引中的所有 key，支持前缀过滤和反向遍历
type Iterator interface {
	// Rewind 重回迭代器的起点（第一个数据）
	Rewind()
	// Seek 根据传入的 key 查找第一个 大于/小于等于 目标的 key，并从此位置开始遍历
	Seek(key []byte)
	// Next 跳转到下一个 key
	Next()
	// Valid 是否有效，是否已经遍历完所有 key，用于退出遍历
	Valid() bool
	// Key 当前遍历位置的 key 数据
	Key() []byte
	// Value 当前遍历位置的 value 数据（LogRecordPos）
	Value() *data.LogRecordPos
	// Close 关闭迭代器，释放相关资源
	Close()
}

// IteratorOptions 迭代器配置项
type IteratorOptions struct {
	// Prefix 遍历前缀为指定值的 key，为空表示遍历所有
	Prefix []byte
	// Reverse 是否反向遍历，默认 false 为正向（从小到大）
	Reverse bool
}

// 为啥把 item放在这里
// Item 是 BTree 索引的存储单元，同时实现 btree.Item 接口
type Item struct {
	key []byte
	pos *data.LogRecordPos
}

func (ai *Item) Less(than btree.Item) bool {
	return bytes.Compare(ai.key, than.(*Item).key) == -1
}
