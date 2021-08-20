// 用法展示
// 开启服务端示例：go run main.go
package main

import (
    "FtpServer/common"
    "crypto/md5"
    "encoding/hex"
    "encoding/json"
    "flag"
    "fmt"
    "io"
    "io/ioutil"
    "log"
    "net/http"
    "os"
    "path"
    "strconv"
    "strings"
)

var configPath = flag.String("configPath", "./etc/config.json", "服务配置文件")
var confs = &common.ServiceConfig{}

func findRetrySeq(dirPath string, metadata *common.ServerFileMetadata) ([]int) {
    slices := []int{}

    // 获取已保存的文件片序号
    storeSeq := make(map[string]bool)
    files, _ := ioutil.ReadDir(dirPath)
    for _, file := range files {
        _, err := strconv.Atoi(file.Name())
        if err != nil {
            fmt.Println("文件片有错", err, file.Name())
            continue
        }
        storeSeq[file.Name()] = true
    }

    i := 0
    for ;i < metadata.SliceNum && len(storeSeq) > 0; i++ {
        indexStr := strconv.Itoa(i)
        if _, ok := storeSeq[indexStr]; ok {
            delete(storeSeq, indexStr)
        } else {
            slices = append(slices, i)
        }
    }

    // -1指代slices的最大数字序号到最后一片都没有收到
    if i < metadata.SliceNum {
        slices = append(slices, i)
        i += 1
        if i < metadata.SliceNum {
            slices = append(slices, -1)
        }
    }

    fmt.Println("还需重传的片", slices)
    return slices
}

func checkFileExist(w http.ResponseWriter, r *http.Request) {
    fid := r.FormValue("fid")
    filename := r.FormValue("filename")

    exist, err := common.CheckFileExist(fid, filename, confs.StoreDir)
    if exist != true || err != nil {
        http.Error(w, "not exist file", http.StatusBadRequest)
    }

    w.WriteHeader(http.StatusOK)
}

func getUploadingStat(w http.ResponseWriter, r *http.Request) {
    fid := r.FormValue("fid")
    filename := r.FormValue("filename")

    tmpDir := path.Join(confs.StoreDir, fid)
    metadataPath := path.Join(confs.StoreDir, filename+".slice")

    // 校验fid和filename是匹配的
    metadata, err := common.LoadMetadata(metadataPath)
    if err != nil || metadata.Fid != fid {
        fmt.Println("文件名和fid对不上，请确认后重试")
        http.Error(w, "not exist file", http.StatusBadRequest)
    }

    retrySeq := common.SliceSeq{
        Slices: []int{},
    }

    if common.IsDir(tmpDir) {
        retrySeq.Slices = findRetrySeq(tmpDir, metadata)
    }

    w.Header().Set("Content-Type", "application/json")
    err = json.NewEncoder(w).Encode(retrySeq)
    if err != nil {
        fmt.Println("编码失败")
        return
    }
    w.WriteHeader(http.StatusOK)
}

func createUploadDir(w http.ResponseWriter, r *http.Request) {
    var cMetadata common.ClientFileMetadata
    err := json.NewDecoder(r.Body).Decode(&cMetadata)
    if err != nil {
        http.Error(w, err.Error(), http.StatusBadRequest)
    }

    // 检查文件是否已存在
    metadataPath := common.GetMetadataFilepath(path.Join(confs.StoreDir, cMetadata.Filename))
    if common.IsFile(metadataPath) {
        http.Error(w, "文件已存在", http.StatusBadRequest)
        return
    }

    uploadDir := path.Join(confs.StoreDir, cMetadata.Fid)
    err = os.Mkdir(uploadDir, 0766)
    if err != nil {
        http.Error(w, err.Error(), http.StatusBadRequest)
        return
    }

    sMetadata := common.ServerFileMetadata{
        ClientFileMetadata: cMetadata,
        State:  "uploading",
    }

    //写元数据文件
    err = common.StoreMetadata(metadataPath, &sMetadata)
    if err != nil {
        http.Error(w, err.Error(), http.StatusBadRequest)
        return
    }
}

// 接收分片文件函数
func receiveSliceFile(w http.ResponseWriter, r *http.Request) {
    var part common.FilePart
    err := json.NewDecoder(r.Body).Decode(&part)
    if err != nil {
        http.Error(w, err.Error(), http.StatusBadRequest)
        return
    }

    sliceFilename := path.Join(confs.StoreDir, part.Fid, strconv.Itoa(part.Index))
    if common.IsFile(sliceFilename) {
        fmt.Printf("%s分片文件已存在，直接丢弃, part.Fid: %s, index: %s\n", sliceFilename, part.Fid, strconv.Itoa(part.Index))
    }

    err = ioutil.WriteFile(sliceFilename, part.Data, 0666)
    if err != nil {
        fmt.Println(err)
        return
    }
}

func mergeSliceFiles(w http.ResponseWriter, r *http.Request) {
    // 不真正进行合并，只计算md5进行数据准确性校验
    var cMetadata common.ClientFileMetadata
    err := json.NewDecoder(r.Body).Decode(&cMetadata)
    if err != nil {
        http.Error(w, err.Error(), http.StatusBadRequest)
        return
    }

    uploadDir := path.Join(confs.StoreDir, cMetadata.Fid)
    hash := md5.New()

    // 计算md5
    for i := 0; i < cMetadata.SliceNum; i++ {
        sliceFilePath := path.Join(uploadDir, strconv.Itoa(i))
        sliceFile, err := os.Open(sliceFilePath)
        if err != nil {
            fmt.Printf("读取文件%s失败, err: %s\n", sliceFilePath, err)
            http.Error(w, err.Error(), http.StatusBadRequest)
            return
        }
        io.Copy(hash, sliceFile)
        sliceFile.Close()
    }

    md5Sum := hex.EncodeToString(hash.Sum(nil))
    if  md5Sum != cMetadata.Md5sum {
        fmt.Println("文件md5校验不通过，数据传输有误，请重新上传文件！")
        fmt.Printf("md5校验失败，原始md5：%s, 计算的md5：%s\n", cMetadata.Md5sum, md5Sum)
        http.Error(w, err.Error(), http.StatusBadRequest)
        // 删除保存文件夹
        os.RemoveAll(uploadDir)
        return
    }

    fmt.Printf("md5校验成功，原始md5：%s, 计算的md5：%s\n", cMetadata.Md5sum, md5Sum)

    // 更新元数据信息
    metadataPath := common.GetMetadataFilepath(path.Join(confs.StoreDir, cMetadata.Filename))
    metadata, err := common.LoadMetadata(metadataPath)
    if err != nil {
        http.Error(w, err.Error(), http.StatusBadRequest)
        return
    }

    metadata.Md5sum = md5Sum
    metadata.State = "active"
    err = common.StoreMetadata(metadataPath, metadata)
    if err != nil {
        fmt.Println("更新元数据文件失败，上传失败")
        http.Error(w, err.Error(), http.StatusBadRequest)
        return
    }

    fmt.Printf("%s上传成功", metadata.Filename)
}

// 处理upload逻辑
func upload(w http.ResponseWriter, r *http.Request) {
    file, handler, err := r.FormFile("filename")
    if err != nil {
        fmt.Println(err)
        return
    }
    defer file.Close()

    f, err := os.OpenFile(path.Join(confs.StoreDir, handler.Filename), os.O_WRONLY|os.O_CREATE, 0666)
    if err != nil {
        fmt.Println(err)
        return
    }
    defer f.Close()

    io.Copy(f, file)
}

func download (w http.ResponseWriter, request *http.Request) {
    //文件名
    filename := request.FormValue("filename")

    //打开文件
    filePath := path.Join(confs.StoreDir, filename)
    file, err := os.Open(filePath)
    if err != nil {
        fmt.Printf("打开文件%s失败, err:%s\n", filePath, err)
        http.Error(w, "文件打开失败", http.StatusBadRequest)
        return
    }
    //结束后关闭文件
    defer file.Close()

    //设置响应的header头
    w.Header().Add("Content-type", "application/octet-stream")
    w.Header().Add("content-disposition", "attachment; filename=\""+filename+"\"")

    //将文件写至responseBody
    _, err = io.Copy(w, file)
    if err != nil {
        http.Error(w, "文件下载失败", http.StatusInternalServerError)
        return
    }
}

func downloadBySlice(w http.ResponseWriter, request *http.Request) {
    filename := request.FormValue("filename")
    sliceIndex := request.FormValue("sliceIndex")

    metadata, err := common.LoadMetadata(common.GetMetadataFilepath(path.Join(confs.StoreDir, filename)))
    if err != nil {
        http.Error(w, "分片文件下载失败", http.StatusBadRequest)
    }

    sliceFile := path.Join(confs.StoreDir, metadata.Fid, sliceIndex)
    if !common.IsFile(sliceFile) {
        fmt.Println("文件切片不存在", sliceFile)
        http.Error(w, "文件异常", http.StatusBadRequest)
        return
    }

    file, err := os.Open(sliceFile)
    if err != nil {
        fmt.Println("打开文件分片失败", sliceFile)
        http.Error(w, "slice read error", http.StatusBadRequest)
        return
    }
    //结束后关闭文件
    defer file.Close()

    //设置响应的header头
    w.Header().Add("Content-type", "application/octet-stream")
    _, err = io.Copy(w, file)
    if err != nil {
        fmt.Printf("下载文件分片%s失败, err:%s", sliceFile, err.Error())
        http.Error(w, "下载文件分片失败", http.StatusBadRequest)
        return
    }
}

// 获取文件元数据信息
func getFileMetainfo(w http.ResponseWriter, request *http.Request) {
    filename := request.FormValue("filename")
    metaPath := common.GetMetadataFilepath(path.Join(confs.StoreDir, filename))
    if !common.IsFile(metaPath) {
        fmt.Println("该文件不存在", metaPath)
        http.Error(w, "file not exist", http.StatusBadRequest)
    }

    metadata, err := common.LoadMetadata(metaPath)
    if err != nil {
        http.Error(w, "文件损坏", http.StatusBadRequest)
    }

    cMetadata := metadata.ClientFileMetadata
    w.Header().Set("Content-Type", "application/json")
    err = json.NewEncoder(w).Encode(cMetadata)
    if err != nil {
        fmt.Println("编码文件基本信息失败")
        http.Error(w, "服务异常", http.StatusInternalServerError)
        return
    }
    w.WriteHeader(http.StatusOK)
}

func getFileInfo(w http.ResponseWriter, request *http.Request) {
    filename := request.FormValue("filename")
    filePath := path.Join(confs.StoreDir, filename)
    if !common.IsFile(filePath) {
        filePath = common.GetMetadataFilepath(filePath)
    }

    fstate, err := os.Stat(filePath)
    if err != nil {
        fmt.Println("读取文件失败", filePath)
        http.Error(w, "读取文件失败", http.StatusBadRequest)
        return
    }

    finfo := common.FileInfo{
        Filename: fstate.Name(),
        Filesize: fstate.Size(),
        Filetype: "normal",
    }

    if strings.HasSuffix(filePath, ".slice") {
        // 切片文件
        metadata, err := common.LoadMetadata(filePath)
        if err != nil {
            http.Error(w, "获取文件元数据信息失败", http.StatusInternalServerError)
        }
        finfo.Filename = metadata.Filename
        finfo.Filesize = metadata.Filesize
        finfo.Filetype = "slice"
    }
    w.Header().Set("Content-Type", "application/json")
    err = json.NewEncoder(w).Encode(finfo)
    if err != nil {
        fmt.Println("编码文件基本信息失败")
        http.Error(w, "服务异常", http.StatusInternalServerError)
        return
    }
    w.WriteHeader(http.StatusOK)
}

// 列出文件信息
func listFiles(w http.ResponseWriter, request *http.Request) {
    files, err := ioutil.ReadDir(confs.StoreDir)
    if err != nil {
        fmt.Println("读文件夹失败", confs.StoreDir)
    }

    fileinfos := common.ListFileInfos{
        Files: []common.FileInfo{},
    }

    for _, file := range files {
        if file.IsDir() {
            continue
        }
        tmpFile := path.Join(confs.StoreDir, file.Name())
        fstate, err := os.Stat(tmpFile)
        if err != nil {
            fmt.Println("读取文件失败")
            continue
        }

        finfo := common.FileInfo{
            Filename: fstate.Name(),
            Filesize: fstate.Size(),
            Filetype: "normal",
        }

        if strings.HasSuffix(file.Name(), ".slice") {
            // 切片文件
            metadata, err := common.LoadMetadata(tmpFile)
            if err != nil {
                continue
            }
            if metadata.State != "active" {
                continue
            }
            finfo.Filename = metadata.Filename
            finfo.Filesize = metadata.Filesize
            finfo.Filetype = "slice"
        }

        fileinfos.Files = append(fileinfos.Files, finfo)
    }

    w.Header().Set("Content-Type", "application/json")
    err = json.NewEncoder(w).Encode(fileinfos)
    if err != nil {
        fmt.Println("压缩文件列表失败")
        http.Error(w, "服务异常", http.StatusBadRequest)
    }
    w.WriteHeader(http.StatusOK)
}

// 记载配置文件
func loadConfig(configPath string) () {
    if !common.IsFile(configPath) {
        log.Panicf("config file %s is not exist", configPath)
    }

    buf, err := ioutil.ReadFile(configPath)
    if err != nil {
        log.Panicf("load config conf %s failed, err: %s\n", configPath, err)
    }

    err = json.Unmarshal(buf, confs)
    if err != nil {
        log.Panicf("decode config file %s failed, err: %s\n", configPath, err)
    }
}
 
func main() {
    flag.Parse()
    loadConfig(*configPath)

    http.HandleFunc("/checkFileExist", checkFileExist)
    http.HandleFunc("/getFileMetainfo", getFileMetainfo)
    http.HandleFunc("/getFileInfo", getFileInfo)
    http.HandleFunc("/listFiles", listFiles)
    http.HandleFunc("/upload", upload)
    http.HandleFunc("/getUploadingStat", getUploadingStat)
    http.HandleFunc("/startUploadSlice", createUploadDir)
    http.HandleFunc("/uploadBySlice", receiveSliceFile)
    http.HandleFunc("/mergeSlice", mergeSliceFiles)
    http.HandleFunc("/download", download)
    http.HandleFunc("/downloadBySlice", downloadBySlice)
    err := http.ListenAndServe(":"+strconv.Itoa(confs.Port), nil)
    if err != nil {
        log.Fatal("ListenAndServe: ", err)
    }
}
