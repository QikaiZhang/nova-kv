我建议你把它当成一个 **TODO（协议健壮性优化）**，先把主流程跑通，后面再一点点补。很多成熟数据库也是这么演进的。

---

# DecodeLogRecordHeader 健壮性优化 TODO

## 1. Header 最小长度检查（必须）

**当前：**

```go
if len(buf) <= 4 {
    return nil, 0
}
```

**问题：**

下一行直接访问：

```go
buf[4]
```

当：

```go
len(buf)==4
```

会直接 panic。

**优化：**

检查 Header 最小长度：

```go
len(buf) >= 5
```

---

## 2. Varint 解码结果检查（必须）

当前：

```go
keySize, n := binary.Varint(...)
```

没有检查：

```go
n
```

根据 Go 官方文档：

```go
n > 0
```

表示成功。

```go
n == 0
```

表示：

数据不足（Buffer 不完整）。

```go
n < 0
```

表示：

Varint 非法。

都应该返回错误。

---

## 3. KeySize 非法值检查（必须）

当前：

```go
header.keySize = uint32(keySize)
```

没有判断：

```go
keySize < 0
```

否则：

```go
uint32(-1)
```

会变成：

```text
4294967295
```

后面可能导致：

```go
make([]byte, keySize)
```

申请超大内存。

---

## 4. ValueSize 非法值检查（必须）

和 KeySize 相同。

包括：

* 是否为负数
* 是否超过允许最大值（例如 MaxValueSize）

---

## 5. Header 是否解析完整（建议）

例如：

Header 被截断：

```text
CRC
Type
KeySize(只写了一半)
```

Varint 解析失败。

应该直接返回：

```go
ErrInvalidHeader
```

而不是继续解析。

---

## 6. Header 最大长度限制（建议）

虽然：

```go
binary.MaxVarintLen32
```

理论上已经限制了。

但是仍建议增加：

```go
headerSize <= maxLogRecordHeaderSize
```

避免异常数据。

---

## 7. CRC 校验（后续）

目前课程：

```go
// TODO
```

以后：

读取完整 Record 后：

```text
Header
↓

Key

↓

Value

↓

重新计算 CRC

↓

比较 Header 中 CRC
```

一致：

继续。

否则：

返回：

```go
ErrCRCMismatch
```

---

## 8. 返回 error（建议）

目前：

```go
func DecodeLogRecordHeader(...)
    (*logRecordHeader, int64)
```

建议改：

```go
func DecodeLogRecordHeader(...)
    (*logRecordHeader, int64, error)
```

这样所有解析失败都有明确原因：

* Header 长度不足
* Varint 非法
* KeySize 非法
* ValueSize 非法
* CRC 校验失败（以后）

而不是：

```go
nil,0
```

调用方根本不知道为什么失败。

---

## 9. Size 合法性检查（建议）

例如：

解析得到：

```text
KeySize

=

3GB
```

但是：

整个 DataFile：

只有：

```text
100MB
```

这种明显异常的数据：

应该尽早返回错误。

避免：

后续：

```go
Read(...)
```

再报 EOF。

---

# 我建议按照优先级排序

| 优先级 | 优化项                  | 是否建议立即做 |
| --- | -------------------- | ------- |
| ⭐⭐⭐ | Header 最小长度检查        | ✅ 必做    |
| ⭐⭐⭐ | Varint 返回值检查         | ✅ 必做    |
| ⭐⭐⭐ | Key/ValueSize < 0 检查 | ✅ 必做    |
| ⭐⭐  | Decode 返回 error      | ✅ 建议    |
| ⭐⭐  | CRC 校验               | 后面实现    |
| ⭐⭐  | Header 完整性检查         | 后面实现    |
| ⭐   | 最大 Size 限制           | 后面实现    |
| ⭐   | 文件剩余长度校验             | 后面实现    |

---

**我还建议再加一个 TODO，很多教程都不会提，但工程里很重要：**

### 10. 自定义错误类型（推荐）

不要到处写：

```go
errors.New("decode failed")
```

统一定义：

```go
var (
    ErrInvalidHeader    = errors.New("invalid log record header")
    ErrInvalidVarint    = errors.New("invalid varint")
    ErrNegativeKeySize  = errors.New("negative key size")
    ErrNegativeValueSize = errors.New("negative value size")
    ErrCRCMismatch      = errors.New("crc mismatch")
)
```

以后：

```go
if errors.Is(err, ErrCRCMismatch) {
    // 可以决定跳过记录、停止恢复或告警
}
```

比字符串比较健壮得多，而且随着你的 KV 项目继续做 Merge、Hint File、甚至以后加 Raft，这套错误体系都可以复用。
