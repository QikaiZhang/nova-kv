package index

import (
	"nova-kv/data"
	"testing"

	"github.com/stretchr/testify/assert"
)

// =============================================================================
// BTree Iterator 单元测试
// =============================================================================

func TestIterator_Rewind_Empty(t *testing.T) {
	bt := NewBTree()
	iter := bt.NewIterator(IteratorOptions{})
	defer iter.Close()

	iter.Rewind()
	assert.False(t, iter.Valid())
	assert.Nil(t, iter.Key())
	assert.Nil(t, iter.Value())
}

func TestIterator_Rewind_Forward(t *testing.T) {
	bt := NewBTree()
	bt.Put([]byte("c"), &data.LogRecordPos{Fid: 1, Offset: 300})
	bt.Put([]byte("a"), &data.LogRecordPos{Fid: 1, Offset: 100})
	bt.Put([]byte("b"), &data.LogRecordPos{Fid: 1, Offset: 200})

	iter := bt.NewIterator(IteratorOptions{})
	defer iter.Close()

	iter.Rewind()

	var keys []string
	for ; iter.Valid(); iter.Next() {
		keys = append(keys, string(iter.Key()))
	}
	assert.Equal(t, []string{"a", "b", "c"}, keys)
}

func TestIterator_Rewind_Reverse(t *testing.T) {
	bt := NewBTree()
	bt.Put([]byte("c"), &data.LogRecordPos{Fid: 1, Offset: 300})
	bt.Put([]byte("a"), &data.LogRecordPos{Fid: 1, Offset: 100})
	bt.Put([]byte("b"), &data.LogRecordPos{Fid: 1, Offset: 200})

	iter := bt.NewIterator(IteratorOptions{Reverse: true})
	defer iter.Close()

	iter.Rewind()

	var keys []string
	for ; iter.Valid(); iter.Next() {
		keys = append(keys, string(iter.Key()))
	}
	assert.Equal(t, []string{"c", "b", "a"}, keys)
}

func TestIterator_Seek_Forward(t *testing.T) {
	bt := NewBTree()
	bt.Put([]byte("c"), &data.LogRecordPos{Fid: 1, Offset: 300})
	bt.Put([]byte("a"), &data.LogRecordPos{Fid: 1, Offset: 100})
	bt.Put([]byte("b"), &data.LogRecordPos{Fid: 1, Offset: 200})
	bt.Put([]byte("e"), &data.LogRecordPos{Fid: 1, Offset: 500})
	bt.Put([]byte("d"), &data.LogRecordPos{Fid: 1, Offset: 400})

	iter := bt.NewIterator(IteratorOptions{})
	defer iter.Close()

	// Seek 到 "b"，应该返回 b, c, d, e
	iter.Seek([]byte("b"))

	var keys []string
	for ; iter.Valid(); iter.Next() {
		keys = append(keys, string(iter.Key()))
	}
	assert.Equal(t, []string{"b", "c", "d", "e"}, keys)
}

func TestIterator_Seek_Reverse(t *testing.T) {
	bt := NewBTree()
	bt.Put([]byte("c"), &data.LogRecordPos{Fid: 1, Offset: 300})
	bt.Put([]byte("a"), &data.LogRecordPos{Fid: 1, Offset: 100})
	bt.Put([]byte("b"), &data.LogRecordPos{Fid: 1, Offset: 200})
	bt.Put([]byte("e"), &data.LogRecordPos{Fid: 1, Offset: 500})
	bt.Put([]byte("d"), &data.LogRecordPos{Fid: 1, Offset: 400})

	iter := bt.NewIterator(IteratorOptions{Reverse: true})
	defer iter.Close()

	// 反向 Seek 到 "d"，应该返回 d, c, b, a
	iter.Seek([]byte("d"))

	var keys []string
	for ; iter.Valid(); iter.Next() {
		keys = append(keys, string(iter.Key()))
	}
	assert.Equal(t, []string{"d", "c", "b", "a"}, keys)
}

func TestIterator_Seek_Exact(t *testing.T) {
	bt := NewBTree()
	bt.Put([]byte("key1"), &data.LogRecordPos{Fid: 1, Offset: 100})
	bt.Put([]byte("key3"), &data.LogRecordPos{Fid: 1, Offset: 300})

	iter := bt.NewIterator(IteratorOptions{})
	defer iter.Close()

	// Seek 到不存在的 key "key2"，应该定位到 "key3"（第一个 >= key2）
	iter.Seek([]byte("key2"))

	var keys []string
	for ; iter.Valid(); iter.Next() {
		keys = append(keys, string(iter.Key()))
	}
	assert.Equal(t, []string{"key3"}, keys)
}

func TestIterator_Seek_BeyondEnd(t *testing.T) {
	bt := NewBTree()
	bt.Put([]byte("a"), &data.LogRecordPos{Fid: 1, Offset: 100})

	iter := bt.NewIterator(IteratorOptions{})
	defer iter.Close()

	// Seek 到超出范围，应该无效
	iter.Seek([]byte("z"))
	assert.False(t, iter.Valid())
}

func TestIterator_Seek_BeforeStart_Reverse(t *testing.T) {
	bt := NewBTree()
	bt.Put([]byte("z"), &data.LogRecordPos{Fid: 1, Offset: 100})

	iter := bt.NewIterator(IteratorOptions{Reverse: true})
	defer iter.Close()

	// 反向 Seek 到比所有 key 都小的位置，应该无效
	iter.Seek([]byte("a"))
	assert.False(t, iter.Valid())
}

func TestIterator_Prefix_Forward(t *testing.T) {
	bt := NewBTree()
	bt.Put([]byte("user:1"), &data.LogRecordPos{Fid: 1, Offset: 100})
	bt.Put([]byte("user:2"), &data.LogRecordPos{Fid: 1, Offset: 200})
	bt.Put([]byte("order:1"), &data.LogRecordPos{Fid: 1, Offset: 300})
	bt.Put([]byte("user:10"), &data.LogRecordPos{Fid: 1, Offset: 400})
	bt.Put([]byte("other"), &data.LogRecordPos{Fid: 1, Offset: 500})

	iter := bt.NewIterator(IteratorOptions{Prefix: []byte("user:")})
	defer iter.Close()

	iter.Rewind()

	var keys []string
	for ; iter.Valid(); iter.Next() {
		keys = append(keys, string(iter.Key()))
	}
	assert.Equal(t, []string{"user:1", "user:10", "user:2"}, keys)
}

func TestIterator_Prefix_Reverse(t *testing.T) {
	bt := NewBTree()
	bt.Put([]byte("user:1"), &data.LogRecordPos{Fid: 1, Offset: 100})
	bt.Put([]byte("user:2"), &data.LogRecordPos{Fid: 1, Offset: 200})
	bt.Put([]byte("order:1"), &data.LogRecordPos{Fid: 1, Offset: 300})

	iter := bt.NewIterator(IteratorOptions{Prefix: []byte("user:"), Reverse: true})
	defer iter.Close()

	iter.Rewind()

	var keys []string
	for ; iter.Valid(); iter.Next() {
		keys = append(keys, string(iter.Key()))
	}
	assert.Equal(t, []string{"user:2", "user:1"}, keys)
}

func TestIterator_Prefix_Seek(t *testing.T) {
	bt := NewBTree()
	bt.Put([]byte("user:1"), &data.LogRecordPos{Fid: 1, Offset: 100})
	bt.Put([]byte("user:2"), &data.LogRecordPos{Fid: 1, Offset: 200})
	bt.Put([]byte("user:3"), &data.LogRecordPos{Fid: 1, Offset: 300})
	bt.Put([]byte("order:1"), &data.LogRecordPos{Fid: 1, Offset: 400})

	iter := bt.NewIterator(IteratorOptions{Prefix: []byte("user:")})
	defer iter.Close()

	iter.Seek([]byte("user:2"))

	var keys []string
	for ; iter.Valid(); iter.Next() {
		keys = append(keys, string(iter.Key()))
	}
	assert.Equal(t, []string{"user:2", "user:3"}, keys)
}

func TestIterator_Value(t *testing.T) {
	bt := NewBTree()
	expectedPos := &data.LogRecordPos{Fid: 2, Offset: 999}
	bt.Put([]byte("key1"), expectedPos)

	iter := bt.NewIterator(IteratorOptions{})
	defer iter.Close()

	iter.Rewind()
	assert.True(t, iter.Valid())
	assert.Equal(t, []byte("key1"), iter.Key())

	pos := iter.Value()
	assert.NotNil(t, pos)
	assert.Equal(t, uint32(2), pos.Fid)
	assert.Equal(t, int64(999), pos.Offset)
}

func TestIterator_Close(t *testing.T) {
	bt := NewBTree()
	bt.Put([]byte("key1"), &data.LogRecordPos{Fid: 1, Offset: 100})

	iter := bt.NewIterator(IteratorOptions{})
	iter.Rewind()
	assert.True(t, iter.Valid())

	iter.Close()
	assert.False(t, iter.Valid())
	assert.Nil(t, iter.Key())
	assert.Nil(t, iter.Value())
}

func TestIterator_Size(t *testing.T) {
	bt := NewBTree()
	assert.Equal(t, 0, bt.Size())

	bt.Put([]byte("a"), &data.LogRecordPos{Fid: 1, Offset: 100})
	assert.Equal(t, 1, bt.Size())

	bt.Put([]byte("b"), &data.LogRecordPos{Fid: 1, Offset: 200})
	assert.Equal(t, 2, bt.Size())

	// 覆盖不增加 Size
	bt.Put([]byte("a"), &data.LogRecordPos{Fid: 2, Offset: 300})
	assert.Equal(t, 2, bt.Size())

	bt.Delete([]byte("a"))
	assert.Equal(t, 1, bt.Size())
}

func TestIterator_NilKey(t *testing.T) {
	bt := NewBTree()
	bt.Put(nil, &data.LogRecordPos{Fid: 1, Offset: 100})

	iter := bt.NewIterator(IteratorOptions{})
	defer iter.Close()

	iter.Rewind()
	assert.True(t, iter.Valid())
	assert.Nil(t, iter.Key())
	assert.NotNil(t, iter.Value())
}

func TestIterator_MultipleRewind(t *testing.T) {
	bt := NewBTree()
	bt.Put([]byte("a"), &data.LogRecordPos{Fid: 1, Offset: 100})
	bt.Put([]byte("b"), &data.LogRecordPos{Fid: 1, Offset: 200})

	iter := bt.NewIterator(IteratorOptions{})
	defer iter.Close()

	iter.Rewind()
	assert.Equal(t, []byte("a"), iter.Key())

	// 多次 Rewind 应该回到开头
	iter.Next()
	assert.Equal(t, []byte("b"), iter.Key())

	iter.Rewind()
	assert.Equal(t, []byte("a"), iter.Key())
}

func TestIterator_NextBeyondEnd(t *testing.T) {
	bt := NewBTree()
	bt.Put([]byte("a"), &data.LogRecordPos{Fid: 1, Offset: 100})

	iter := bt.NewIterator(IteratorOptions{})
	defer iter.Close()

	iter.Rewind()
	assert.True(t, iter.Valid())

	iter.Next()
	assert.False(t, iter.Valid())

	// Next 超出范围后继续调用 Next，应保持无效
	iter.Next()
	assert.False(t, iter.Valid())
	assert.Nil(t, iter.Key())
}

func TestIterator_ConcurrentIterator(t *testing.T) {
	bt := NewBTree()
	for i := 0; i < 100; i++ {
		bt.Put([]byte{byte(i)}, &data.LogRecordPos{Fid: 1, Offset: int64(i * 100)})
	}

	// 并发创建迭代器并遍历
	done := make(chan bool, 3)
	for g := 0; g < 3; g++ {
		go func() {
			iter := bt.NewIterator(IteratorOptions{})
			defer iter.Close()
			count := 0
			for iter.Rewind(); iter.Valid(); iter.Next() {
				count++
			}
			assert.Equal(t, 100, count)
			done <- true
		}()
	}

	for i := 0; i < 3; i++ {
		<-done
	}
}
