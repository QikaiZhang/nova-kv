package index

import (
	"bytes"
	"nova-kv/data"

	"github.com/google/btree"
)

// btreeIterator BTree 索引的迭代器实现
// 核心思路：因为索引全在内存中，Rewind/Seek 时一次性收集所有匹配的 item 到切片，
// 然后通过游标遍历。这样即使在遍历过程中索引被修改，迭代器视图也不会受影响。
type btreeIterator struct {
	tree   *BTree
	opts   IteratorOptions
	items  []*Item // 收集到的匹配 item（已排序且去重）
	cursor int     // 当前游标位置，-1 表示未初始化
}

// NewIterator 创建 BTree 迭代器
func (bt *BTree) NewIterator(opts IteratorOptions) Iterator {
	return &btreeIterator{
		tree:   bt,
		opts:   opts,
		items:  nil,
		cursor: -1,
	}
}

// Rewind 回到迭代器起点，收集所有匹配前缀的 item
func (it *btreeIterator) Rewind() {
	it.items = nil
	it.cursor = -1

	it.tree.lock.RLock()
	defer it.tree.lock.RUnlock()

	if it.opts.Reverse {
		// 反向遍历：从大到小
		it.tree.btree.Descend(func(item btree.Item) bool {
			i := item.(*Item)
			if it.opts.Prefix != nil && !bytes.HasPrefix(i.key, it.opts.Prefix) {
				return true // 继续遍历，前缀不匹配的跳过但不停止
			}
			it.items = append(it.items, i)
			return true
		})
	} else {
		// 正向遍历：从小到大
		it.tree.btree.Ascend(func(item btree.Item) bool {
			i := item.(*Item)
			if it.opts.Prefix != nil && !bytes.HasPrefix(i.key, it.opts.Prefix) {
				return true
			}
			it.items = append(it.items, i)
			return true
		})
	}

	if len(it.items) > 0 {
		it.cursor = 0
	}
}

// Seek 定位到第一个 >= key（正向）或 <= key（反向）的位置
// 根据 IteratorOptions.Reverse 决定查找方向
func (it *btreeIterator) Seek(key []byte) {
	it.items = nil
	it.cursor = -1

	it.tree.lock.RLock()
	defer it.tree.lock.RUnlock()

	pivotItem := &Item{key: key}

	if it.opts.Reverse {
		// 反向：找 <= key 的所有 item
		it.tree.btree.DescendLessOrEqual(pivotItem, func(item btree.Item) bool {
			i := item.(*Item)
			if it.opts.Prefix != nil && !bytes.HasPrefix(i.key, it.opts.Prefix) {
				return true
			}
			it.items = append(it.items, i)
			return true
		})
	} else {
		// 正向：找 >= key 的所有 item
		it.tree.btree.AscendGreaterOrEqual(pivotItem, func(item btree.Item) bool {
			i := item.(*Item)
			if it.opts.Prefix != nil && !bytes.HasPrefix(i.key, it.opts.Prefix) {
				return true
			}
			it.items = append(it.items, i)
			return true
		})
	}

	if len(it.items) > 0 {
		it.cursor = 0
	}
}

// Next 移动到下一个 key
// 调用方应在 Valid() == true 时调用
func (it *btreeIterator) Next() {
	if it.cursor < 0 || it.cursor >= len(it.items) {
		return
	}
	it.cursor++
}

// Valid 当前位置是否有效
// true：还有数据可遍历；false：已遍历完或未初始化
func (it *btreeIterator) Valid() bool {
	return it.cursor >= 0 && it.cursor < len(it.items)
}

// Key 返回当前遍历位置的 key
// 仅在 Valid() == true 时调用有意义
func (it *btreeIterator) Key() []byte {
	if !it.Valid() {
		return nil
	}
	return it.items[it.cursor].key
}

// Value 返回当前遍历位置的 LogRecordPos
// 仅在 Valid() == true 时调用有意义
func (it *btreeIterator) Value() *data.LogRecordPos {
	if !it.Valid() {
		return nil
	}
	return it.items[it.cursor].pos
}

// Close 关闭迭代器，清理资源
func (it *btreeIterator) Close() {
	it.items = nil
	it.cursor = -1
}

// Size 返回 BTree 索引中 key 的数量
func (bt *BTree) Size() int {
	bt.lock.RLock()
	defer bt.lock.RUnlock()
	return bt.btree.Len()
}

// 确保 btreeIterator 实现了 Iterator 接口
var _ Iterator = (*btreeIterator)(nil)
