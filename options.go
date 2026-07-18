package novakv

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
}

// DefaultOptions 默认配置
var DefaultOptions = Options{
	DirPath:      "/tmp/nova-kv",
	DataFileSize: 256 * 1024 * 1024, // 256MB
	SyncWrites:   false,
}
