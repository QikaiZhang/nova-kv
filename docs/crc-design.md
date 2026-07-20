# CRC 校验设计

## 协议布局

磁盘上的每条日志记录格式：

```
+-------+--------+-----------+-------------+-----+-------+
| CRC   |  Type  |  KeySize  |  ValueSize  | Key | Value |
+-------+--------+-----------+-------------+-----+-------+
   4B      1B      变长(varint) 变长(varint)   变长   变长
   0-3      4       5+           ...          ...   ...
```

## CRC 校验范围

CRC 校验的是**从第 5 字节开始到 Value 末尾的全部内容**，即跳过 CRC 自身的前 4 字节。

```
编码时：  crc = crc32.ChecksumIEEE(encBytes[4:])
解码时：  storedCRC  = binary.LittleEndian.Uint32(buf[:4])
         computedCRC = crc32.ChecksumIEEE(buf[4:])
         比对 storedCRC != computedCRC → 数据损坏
```

### 为什么校验 encBytes[4:] 而不是从 0 开始？

CRC 自己占了前 4 字节。如果把 CRC 也纳入校验范围，就变成一个循环依赖——任何 CRC 值算出来写进去后，下次读取再算会不一样。

## 写路径 (EncodeLogRecord)

1. 预分配 header 最大空间 (13 字节)
2. header[4] 写入 Type
3. header[5:] 写入 KeySize、ValueSize（变长编码）
4. 拼接 encBytes = header 前 index 字节 + key + value
5. **CRC = crc32.ChecksumIEEE(encBytes[4:])**，写入 encBytes[:4]

## 读路径 (ReadLogRecord)

1. **边界检查**：offset ≥ fileSize → EOF；offset + headerReadSize > fileSize → 只读剩余部分
2. 读 header → 解码拿到 headerSize, keySize, valueSize, storedCRC
3. 读 key + value
4. 拼 recordData = headerBuf[4:headerSize] + kvBuf
5. **computedCRC = crc32.ChecksumIEEE(recordData)**，比照 storedCRC
6. 不匹配 → 返回 CRC mismatch 错误

## 错误处理

| 场景 | 行为 |
|------|------|
| offset 超出文件 | 返回 io.EOF |
| header 被截断 | DecodeLogRecordHeader 返回 varint 截断错误 |
| keySize/valueSize 为负 | DecodeLogRecordHeader 返回错误 |
| CRC 不匹配 | 返回 CRC mismatch error |
| 上层 loadIndexFromDataFiles | 收到 CRC 错误后 break 跳过当前文件剩余部分 |

## 关键设计决策

1. **CRC32 (IEEE)**：选择 CRC32 而非更重的 hash，因为 CRC32 是硬件友好的校验算法，对磁盘静默损坏（bit rot）足够敏感。
2. **Little Endian**：与 x86/ARM 平台内存顺序一致，零拷贝场景下无需字节序转换。
3. **一次性读全记录**：拿到 headerSize + kvSize 后再拼接，确保 CRC 对一个连续 buffer 计算，避免分段拼接错误。
