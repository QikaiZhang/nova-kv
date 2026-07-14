package index

import (
	"bytes"
	"nova-kv/data"

	"github.com/google/btree"
)

type Indexer interface {
	//Put 像索引中存储 key 对应的数据是位置信息
	Put(key []byte, pos *data.LogRecordPos) bool
	//Get 根据 key 取出数据的位置信息
	Get(key []byte) *data.LogRecordPos
	// Delete 根据 key 删除索引
	Delete(key []byte) bool
}

// 为啥把 item放在这里
type Item struct {
	key []byte
	pos *data.LogRecordPos
}

func (ai *Item) Less(than btree.Item) bool {
	return bytes.Compare(ai.key, than.(*Item).key) == -1
}
