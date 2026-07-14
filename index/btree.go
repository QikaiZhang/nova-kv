package index

import (
	"nova-kv/data"
	"sync"

	"github.com/google/btree"
)

type BTree struct {
	btree *btree.BTree
	lock  *sync.RWMutex //并发不安全->加锁保护
}

func NewBTree() *BTree {
	return &BTree{
		btree: btree.New(32),     //叶子节点数量
		lock:  new(sync.RWMutex), //并发安全
	}
}

func (bt *BTree) Put(key []byte, pos *data.LogRecordPos) bool {
	item := &Item{
		key: key,
		pos: pos,
	}
	bt.lock.Lock()
	bt.btree.ReplaceOrInsert(item)
	bt.lock.Unlock()
	return true
}

func (bt *BTree) Get(key []byte) *data.LogRecordPos {
	item := &Item{key: key}
	resItem := bt.btree.Get(item)

	if resItem == nil {
		return nil
	}
	return resItem.(*Item).pos //这个返回值没太懂 resItem到底是个啥
}
func (bt *BTree) delete(key []byte) bool {
	item := &Item{key: key}
	bt.lock.Lock()
	oldItem := bt.btree.Delete(item)
	bt.lock.Unlock()
	if oldItem == nil {
		return false
	}
	return true
}
