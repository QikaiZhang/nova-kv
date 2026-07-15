package novakv

// IndexType 索引实现类型
// 当前仅支持 BTree，后续可扩展 ART、SkipList 等
type IndexType = int8

const (
	// BTree Google BTree 实现（默认）
	BTree IndexType = iota + 1
)

// Options 数据库启动配置
type Options struct {
	// DirPath 数据目录路径
	DirPath string

	// DataFileSize 单个数据文件的大小阈值
	// 超过此大小后，当前 active file 转为 immutable，新建文件
	DataFileSize int64

	// SyncWrites 每次写入是否强制刷盘
	// true: 强一致，性能差
	// false: 高性能，崩溃可能丢最近几秒数据
	SyncWrites bool

	// IndexType 索引实现类型
	// 1 = BTree，后续可扩展 ART / SkipList
	IndexType IndexType
}

// DefaultOptions 默认配置
var DefaultOptions = Options{
	DirPath:      "/tmp/nova-kv",
	DataFileSize: 256 * 1024 * 1024, // 256MB
	SyncWrites:   false,
	IndexType:    BTree,
}
