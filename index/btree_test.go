package index

import (
	"nova-kv/data"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestBTree_Put(t *testing.T) {
	bt := NewBTree()

	// 测试插入 nil key
	res := bt.Put(nil, &data.LogRecordPos{PosID: 1, Offset: 100})
	assert.True(t, res)

	// 测试插入正常 key
	res2 := bt.Put([]byte("a"), &data.LogRecordPos{PosID: 1, Offset: 2})
	assert.True(t, res2)

	// 测试插入相同 key（覆盖）
	res3 := bt.Put([]byte("a"), &data.LogRecordPos{PosID: 2, Offset: 200})
	assert.True(t, res3)
}

func TestBTree_Get(t *testing.T) {
	bt := NewBTree()

	// 测试获取不存在的 key
	pos := bt.Get([]byte("not_exist"))
	assert.Nil(t, pos)

	// 插入数据后再获取
	expectedPos := &data.LogRecordPos{PosID: 1, Offset: 100}
	bt.Put([]byte("key1"), expectedPos)

	pos = bt.Get([]byte("key1"))
	assert.NotNil(t, pos)
	assert.Equal(t, expectedPos.PosID, pos.PosID)
	assert.Equal(t, expectedPos.Offset, pos.Offset)

	// 测试覆盖后的获取
	newPos := &data.LogRecordPos{PosID: 2, Offset: 200}
	bt.Put([]byte("key1"), newPos)

	pos = bt.Get([]byte("key1"))
	assert.NotNil(t, pos)
	assert.Equal(t, newPos.PosID, pos.PosID)
	assert.Equal(t, newPos.Offset, pos.Offset)
}

func TestBTree_Delete(t *testing.T) {
	bt := NewBTree()

	// 测试删除不存在的 key
	res := bt.delete([]byte("not_exist"))
	assert.False(t, res)

	// 插入后删除
	bt.Put([]byte("key1"), &data.LogRecordPos{PosID: 1, Offset: 100})
	res = bt.delete([]byte("key1"))
	assert.True(t, res)

	// 验证已删除
	pos := bt.Get([]byte("key1"))
	assert.Nil(t, pos)

	// 重复删除
	res = bt.delete([]byte("key1"))
	assert.False(t, res)
}

func TestBTree_Concurrency(t *testing.T) {
	bt := NewBTree()

	// 并发写入测试
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			key := []byte{byte(i)}
			bt.Put(key, &data.LogRecordPos{PosID: uint32(i), Offset: uint64(i * 100)})
		}(i)
	}
	wg.Wait()

	// 验证数据
	for i := 0; i < 100; i++ {
		key := []byte{byte(i)}
		pos := bt.Get(key)
		assert.NotNil(t, pos)
		assert.Equal(t, int64(i), pos.PosID)
		assert.Equal(t, int64(i*100), pos.Offset)
	}

	// 并发读取测试
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			key := []byte{byte(i)}
			pos := bt.Get(key)
			assert.NotNil(t, pos)
		}(i)
	}
	wg.Wait()
}

func TestBTree_ItemTypeAssertion(t *testing.T) {
	bt := NewBTree()

	// 测试 Get 返回值的类型断言
	bt.Put([]byte("test"), &data.LogRecordPos{PosID: 1, Offset: 100})

	item := &Item{key: []byte("test")}
	resItem := bt.btree.Get(item)

	// resItem 是 btree.Item 接口类型
	// 需要通过类型断言转换为 *Item
	assert.NotNil(t, resItem)

	// 类型断言为 *Item
	itemResult, ok := resItem.(*Item)
	assert.True(t, ok)
	assert.Equal(t, []byte("test"), itemResult.key)
	assert.Equal(t, int64(1), itemResult.pos.PosID)
	assert.Equal(t, int64(100), itemResult.pos.Offset)
}
