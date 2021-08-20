package common

import "time"

// ServiceConfig 配置文件结构
type ServiceConfig struct {
	Port int
	Address string
	StoreDir string
}

// FilePart 文件分片结构
type FilePart struct{
	Fid     string  // 操作文件ID，随机生成的UUID
	Index   int     // 文件切片序号
	Data    []byte  // 分片数据
}

// ClientFileMetadata 客户端传来的文件元数据结构
type ClientFileMetadata struct {
	Fid         string      // 操作文件ID，随机生成的UUID
	Filesize    int64       // 文件大小（字节单位）
	Filename    string      // 文件名称
	SliceNum    int         // 切片数量
	Md5sum      string      // 文件md5值
	ModifyTime  time.Time   // 文件修改时间
}

// ServerFileMetadata 服务端保存的文件元数据结构
type ServerFileMetadata struct {
	ClientFileMetadata  // 隐式嵌套
	State   string      // 文件状态，目前有uploading、downloading和active
}

type SliceSeq struct {
	Slices  []int   // 需要重传的分片号
}

// FileInfo 文件列表单元结构
type FileInfo struct {
	Filename    string  // 文件名
	Filesize    int64   // 文件大小
	Filetype    string  // 文件类型（目前有普通文件和切片文件两种）
}

// ListFileInfos 文件列表结构
type ListFileInfos struct {
	Files    []FileInfo
}
