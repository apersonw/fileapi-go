# FileApi Go SDK

FileApi Open API 的官方 Go 客户端：请求签名、代理上传、直传与分片直传、游标列表、下载与访问链接。

- 只依赖标准库，Go 1.23+（`ListAll` 使用迭代器）
- `Client` 可并发使用；所有方法接受 `context.Context`，用它控制超时与取消
- 与服务端、管理后台共用同一组签名测试向量，保证算法一致

## 安装

```bash
go get github.com/apersonw/fileapi-go@latest
```

```go
import "github.com/apersonw/fileapi-go/fileapi"
```

文档见 [pkg.go.dev](https://pkg.go.dev/github.com/apersonw/fileapi-go/fileapi)。许可证为 [Apache-2.0](LICENSE)。

## 快速开始

```go
import "github.com/apersonw/fileapi-go/fileapi"

c, err := fileapi.New("https://files.example.com", os.Getenv("FILEAPI_CLIENT_ID"), os.Getenv("FILEAPI_CLIENT_SECRET"))

// 代理上传（经 FileApi 写入存储），关联业务对象
res, err := c.UploadFile(ctx, "./avatar.png", &fileapi.UploadOptions{ObjectType: "user_avatar", ObjectID: "42"})

// 统一入口链接（与底层存储解耦，迁移存储后仍然有效）
link, err := c.AccessURL(ctx, res.ID, &fileapi.URLOptions{Expires: time.Hour})
```

`baseURL` 可以带反向代理前缀（如 `https://example.com/api`），前提是代理转发时去掉该前缀——签名只覆盖服务端看到的 `/open/v1/...` 路径。

## 能力一览

| 方法 | 说明 |
|---|---|
| `Upload` / `UploadFile` | 代理上传。流式发送不占内存，边发边算 SHA-256 附在表单末尾，服务端校验不一致时拒绝；长度已知时带 `Content-Length`，未知时分块发送 |
| `UploadDirect` / `UploadFileDirect` | 直传存储平台（不经过 FileApi）。达到 `MultipartThreshold`（默认 64MiB）自动分片并发上传：分片地址不足或将过期时自动补签，单个分片失败重试 3 次；失败时删除待确认记录以释放配额（设置 `Resumable` 时保留，可断点续传） |
| `MultipartStatus` / `ResumeUpload` / `ResumeUploadFile` | 分片直传进度；从中断处继续分片直传 |
| `UploadInstant` / `HashSHA256` | 秒传：按内容 SHA-256 复用本客户端已有的相同文件，不传输内容；`Hit=false` 时需正常上传 |
| `Get` / `List` / `ListAll` | 详情、单页查询（游标或页码）、按游标遍历全部（`iter.Seq2`） |
| `BatchGet` / `BatchDelete` / `ResolveNames` | 批量接口，超过服务端单次上限时自动分批 |
| `Open` / `OpenRange` / `Download` / `DownloadFile` | 代理下载与区间下载；`DownloadFile` 先写临时文件，完整后再改名 |
| `AccessURL` | 签发访问链接：统一入口（默认）或存储直链、有效期、附件下载、缩略图、按需图片处理（`Image`） |
| `Rename` / `SetExpiration` | 修改原始文件名、设置或取消过期时间 |
| `BatchRenew` / `BatchSetExpiration` | 批量续期 / 批量设置或取消过期时间（最多 1000 个） |
| `SimilarImages` / `SimilarImageGroups` | 相似图片：与某张图片相似的图片 / 按相似度分组找出重复图片（见下文"相似图片"） |
| `UpdateMetadata` | 修改元数据（合并设置、删除键；列表可按元数据筛选） |
| `Delete` / `DeletePermanently` / `BatchDeletePermanently` | 删除（服务端开启回收站时移入回收站）/ 彻底删除 |
| `ListTrash` / `Restore` / `EmptyTrash` | 回收站：列表、恢复、清空（见下文"回收站"） |
| `CreateArchive` | 批量打包下载链接（ZIP，见下文"批量打包下载"） |
| `LinkStatus` / `RevokeLink` | 限次 / 可吊销 / 带提取码链接的状态与吊销（见下文"限次 / 可吊销链接"） |
| `ListVersions` / `OpenVersion` / `RestoreVersion` / `DeleteVersion` | 文件版本历史（上传新版本用 `VersionOf`，见下文"文件版本历史"） |
| `Video` / `Transcode` / `WaitVideo` / `CancelTranscode` / `DeleteVideo` / `Play` | 视频转码为多清晰度 HLS 与播放地址（上传时用 `Transcode`，见下文"视频转码与播放"） |
| `ListFolders` / `MoveFiles` / `MoveFile` / `RenameFolder` / `DeleteFolder` | 文件夹（虚拟目录）：列出子目录、移动文件、重命名 / 删除目录（见下文"文件夹"） |
| `HotFiles` | 热门文件：最近 N 天访问次数最多的文件；`Get` / `List` 返回的 `File.AccessCount` / `LastAccessedAt` 为累计访问次数与最近访问时间 |
| `NewUploadToken` | 本地签发前端上传令牌，浏览器 / 小程序凭令牌直接上传（见下文"前端上传令牌"） |

### 直传与分片直传

```go
f, err := c.UploadFileDirect(ctx, "./backup.tar.gz", &fileapi.DirectUploadOptions{
	PartSize:    16 << 20, // 默认由服务端决定
	Concurrency: 8,        // 默认 4
	Progress:    func(sent, total int64) { fmt.Printf("\r%d%%", sent*100/total) },
})
```

直传需要调用方能访问存储平台的 Endpoint；存储只在内网可达时用代理上传。

### 断点续传

设置 `Resumable` 后分片直传失败时保留已上传的分片，返回 `*fileapi.ResumableError`；保存其中的 `FileID`（如写入数据库），
进程重启后用 `ResumeUploadFile` 继续：SDK 查询已上传的分片，用其 MD5 ETag 校验本地内容（不一致的重新上传），只上传其余分片后合并。
须在服务端清理未完成上传之前完成（`MultipartStatus(...).ExpiresAt`，默认创建后 24 小时）。

```go
f, err := c.UploadFileDirect(ctx, "./backup.tar.gz", &fileapi.DirectUploadOptions{Resumable: true})
var re *fileapi.ResumableError
if errors.As(err, &re) {
	// 保存 re.FileID，稍后（可在另一个进程中）继续
	f, err = c.ResumeUploadFile(ctx, re.FileID, "./backup.tar.gz", nil)
}
```

### 秒传

服务端开启去重（`upload.dedup`，默认开启）时，本客户端已上传过相同内容的文件可以秒传：设置 `TryInstant` 后 SDK 先读一遍内容计算 SHA-256，
命中则直接登记新文件、不再上传（`UploadResult.Instant=true`），未命中再按原方式上传。代理上传需要 `r` 实现 `io.Seeker` 且长度已知（`UploadFile` 满足）。

```go
res, err := c.UploadFile(ctx, "./report.pdf", &fileapi.UploadOptions{TryInstant: true})
f, err := c.UploadFileDirect(ctx, "./backup.tar.gz", &fileapi.DirectUploadOptions{TryInstant: true})
```

只会命中本客户端经代理上传或秒传的文件（服务端开启 `upload.dedup_direct_max_size` 后也包括直传）。即使不秒传，代理上传的内容与已有文件相同时服务端也会复用已有对象（`Deduplicated=true`）。

### 图片处理

`URLOptions.Image` 让服务端按宽高、缩放方式、格式与质量处理图片（JPEG / PNG / GIF / WebP），处理结果由服务端缓存，同一参数只处理一次：

```go
link, err := c.AccessURL(ctx, id, &fileapi.URLOptions{
	Image: &fileapi.ImageOptions{Width: 300, Height: 300, Fit: fileapi.FitCover, Format: fileapi.FormatJPEG, Quality: 80},
})
```

输出格式可选 `FormatJPEG`、`FormatPNG`、`FormatWebP`（有损 WebP，保留透明通道，同等质量下通常比 JPEG 更小）。

`ImageOptions.Watermark` 加文字或图片水印（二选一，可不缩放单独使用）。图片水印先像普通文件一样上传，再用它的文件 ID：

```go
logo, err := c.UploadFile(ctx, "./logo.png", nil)
link, err := c.AccessURL(ctx, id, &fileapi.URLOptions{
	Image: &fileapi.ImageOptions{Width: 1080, Watermark: &fileapi.Watermark{ImageID: logo.ID, Position: fileapi.PositionSE, Opacity: 80}},
})
// 倾斜平铺的文字水印
link, err = c.AccessURL(ctx, id, &fileapi.URLOptions{
	Image: &fileapi.ImageOptions{Watermark: &fileapi.Watermark{Text: "内部资料", Position: fileapi.PositionTile, Opacity: 30}},
})
```

每组不同的参数（包括不同的水印文字）服务端各缓存一份结果，不要为每个访问者生成不同的文字。

### 过期时间

上传选项 `ExpiresIn`（`UploadOptions` / `DirectUploadOptions` / `InstantOptions`）设置多久后过期，到期后服务端自动删除；`File.ExpiresAt` 为过期时间。
已上传的文件用 `SetExpiration` 修改，传零值取消过期：

```go
res, err := c.UploadFile(ctx, "./export.csv", &fileapi.UploadOptions{ExpiresIn: 24 * time.Hour})
f, err := c.SetExpiration(ctx, res.ID, time.Now().Add(7*24*time.Hour))
f, err = c.SetExpiration(ctx, res.ID, time.Time{}) // 取消过期
```

即将到期的文件：`ListQuery{ExpiringDays: 7}`（7 天内到期删除）/ `ListQuery{RetentionExpiringDays: 7}`（7 天内保留锁定到期），批量续期：

```go
page, err := c.List(ctx, &fileapi.ListQuery{ExpiringDays: 7})
n, err := c.BatchRenew(ctx, ids, 30*24*time.Hour)          // 从现在起 30 天后过期
n, err = c.BatchSetExpiration(ctx, ids, time.Time{})       // 取消过期
```

到期前（服务端系统设置 `expiry_alert.days_before`，默认 3 天）推送 `EventFileExpiring` / `EventFileRetentionExpiring`，用 `e.Expiring()` 解析（`Files` 为即将到期的文件）。

### 相似图片

服务端后台为图片计算感知哈希，`Distance`（0 ~ 64）越小越相似，缩放、重新压缩后仍能识别；新上传的图片稍后才能参与比较（`Hashed` 为 false 时稍后再查）：

```go
res, err := c.SimilarImages(ctx, id, nil)                                        // 默认阈值 8，按距离从近到远
groups, err := c.SimilarImageGroups(ctx, &fileapi.SimilarOptions{Folder: "/相册/"}) // 重复图片分组，每组第一张为最新的
```

### 列表

```go
// 遍历（按游标，不做 OFFSET 翻页，数据量大也不变慢）
for f, err := range c.ListAll(ctx, &fileapi.ListQuery{Category: fileapi.CategoryImage}) {
	if err != nil {
		return err
	}
	fmt.Println(f.ID, f.OriginalFilename)
}

// 手动翻页
page, err := c.List(ctx, &fileapi.ListQuery{PageSize: 50, WithTotal: true})
next, err := c.List(ctx, &fileapi.ListQuery{PageSize: 50, Cursor: page.NextCursor})
```

### 元数据检索与修改

按元数据筛选用 `ListQuery.Metadata` / `MetadataKeys`（文件须包含全部键值 / 须包含这些键）；`UpdateMetadata(ctx, id, set, remove...)` 与已有元数据合并修改，remove 中的键删除，其余键不变（最多 20 项）。

### 视频转码与播放

上传时设置 `UploadOptions.Transcode`（或 `DirectUploadOptions` / `InstantOptions` / `UploadTokenOptions` 的 `Transcode`、`Profiles`），
服务端的 transcoder 把视频转为多档 HLS；`WaitVideo` 等待完成，`Play` 签发 HLS 主清单地址（hls.js、Safari / iOS 可直接播放）。

```go
f, err := client.UploadFile(ctx, "/tmp/movie.mp4", &fileapi.UploadOptions{Transcode: true}) // 或 Profiles: []string{"720p", "1080p"}
v, err := client.WaitVideo(ctx, f.ID, 0)                       // v.Status == fileapi.VideoReady，v.Renditions 为各档
p, err := client.Play(ctx, f.ID, &fileapi.PlayOptions{Expires: 2 * time.Hour})
// p.Playable 时 p.URL 为 .../v/{token}/master.m3u8；尚未转码完成时 p.FallbackURL 为原文件地址
v, err = client.Transcode(ctx, f.ID, "360p", "720p")            // 换档位重新转码（完成前仍可播放旧产物）
```

字幕：`PutSubtitle(ctx, id, "zh-CN", srtOrVTT, &fileapi.SubtitleOptions{Label: "简体中文", Default: true})` 上传或替换某一语言的字幕（SRT 自动转为 WebVTT，每个文件最多 20 条），`Subtitles` / `Subtitle` / `DeleteSubtitle` 查询与删除；有可播放产物时主清单自动带上字幕分组，`Play` 返回 `Subtitles`。

播放地址提取码：`PlayOptions{Password: "Ab12"}` 签发的地址 `p.PasswordProtected` 为 true，不能直接播放；播放页收集提取码后 `POST {URL 所在目录}/unlock`（`{"password": "…"}`）换取带凭据、可直接播放的地址。

HLS 加密：服务端开启 `video.encrypt`（或客户端 `video_encrypt`）时分片经 AES-128 加密，`v.Encrypted` 为 true；播放器按媒体清单的 `EXT-X-KEY` 自动从播放地址同一令牌的 `/key` 取密钥，接入方无需改动。

多编码、水印与 DASH：`TranscodeWith(ctx, id, &fileapi.TranscodeOptions{Codecs: []string{fileapi.CodecH264, fileapi.CodecHEVC}, Watermark: &fileapi.VideoWatermark{Text: "版权所有", Position: "ne"}})`
按编码各输出一套档位（`720p` / `720p-hevc` / `720p-av1`，`Rendition.Codec`），烧录文字或图片（`ImageFileID`）水印（不指定时用客户端默认水印，`NoWatermark` 不加）；
新产物音视频分离（音频档名为 `audio`），`v.Dash` 为 true 时 `Play` 返回 `DashURL`（DASH 清单，dash.js / Shaka / ExoPlayer 可播放；加密的产物只有 HLS）。

拖动预览：服务端开启 `video.thumbnails`（默认开启）时转码顺带生成雪碧图，`v.Thumbnails` 为间隔、单格尺寸与数量，`p.ThumbnailsURL` 为 WebVTT 索引（每条为雪碧图地址 `#xywh=x,y,w,h`，可交给 Video.js / Plyr / Vidstack 等播放器的缩略图预览）。

转码完成 / 失败推送 `EventVideoReady` / `EventVideoFailed`（`e.Video()` 解析）；`File.Video` 带转码状态，`Usage.VideoBytes` 为产物占用。

### 文件夹

文件可以归入虚拟目录（如 `/合同/2026`）：上传时设置 `UploadOptions.Folder` / `DirectUploadOptions.Folder` / `InstantOptions.Folder` / `UploadTokenOptions.Folder`，
`File.Folder` 为所在目录（以 `/` 开头和结尾，根目录为 `/`）。目录随文件存在，移动与重命名只修改目录字段，不改动存储对象与链接。

```go
f, err := client.UploadFile(ctx, "/tmp/甲方.pdf", &fileapi.UploadOptions{Folder: "/合同/2026"})
list, err := client.ListFolders(ctx, "/合同")                              // 子目录（含其中的文件数与大小）
page, err := client.List(ctx, &fileapi.ListQuery{Folder: "/合同", Recursive: true}) // 目录中的文件（含下级目录）
n, err := client.MoveFiles(ctx, []int64{1, 2}, "/归档")
n, err = client.RenameFolder(ctx, "/合同", "/法务/合同")                    // 下级目录与回收站中的文件一并修改
arch, err := client.CreateArchive(ctx, nil, &fileapi.ArchiveOptions{Folder: "/法务"}) // 打包整个目录
res, err := client.DeleteFolder(ctx, "/临时", false)                         // res.Remaining > 0 时再次调用
```

文件清单导出：`ExportFiles(ctx, &fileapi.ListQuery{Folder: "/合同", Recursive: true}, w)` 按与 List 相同的条件流式导出 CSV（带 BOM），返回导出的行数与是否截断（服务端一次最多 100 万行）。

文件标签：`CreateTag(ctx, "合同", fileapi.TagBlue)` 新建标签，`SetFileTags(ctx, id, []int64{tag.ID})` 设置文件的标签（替换），`BatchTag(ctx, ids, add, remove)` 批量添加 / 移除，`ListTags` / `UpdateTag` / `DeleteTag` 管理标签库；`File.Tags` 为文件的标签，`ListQuery{TagIDs: …, TagAny: true}` 按标签筛选。自动打标签规则：`CreateTagRule(ctx, fileapi.TagRule{Name: "合同 PDF", TagIDs: []int64{tag.ID}, Extensions: []string{"pdf"}, Folder: "/合同/", Recursive: true, Enabled: true})` 之后上传的新文件满足条件时自动打标签，`PreviewTagRule` 试运行、`ApplyTagRule` 给已有文件补打（`Remaining` 时再次调用），`ListTagRules` / `UpdateTagRule` / `DeleteTagRule` 管理规则。上传时直接打标签：`UploadOptions` / `DirectUploadOptions` / `InstantOptions` / `UploadTokenOptions` 的 `TagIDs`（最多 20 个，不能与 `VersionOf` 同时使用）；`ShareTags(ctx, []int64{tag.ID}, nil)` 分享带有全部这些标签的文件（创建时取当时的文件，之后改标签不影响该分享）。

月度用量（月度账单）：`UsageReport(ctx, &fileapi.UsageReportOptions{Month: "2026-09"})` 返回该月的存储（月末、峰值、按天平均）、上传与删除、下载流量与转码用量，`From` / `To` 查询多个月（最多 12 个），`UsageReportCSV` 返回 CSV（带 BOM，Excel 可直接打开）。

压缩包预览：`ZipEntries(ctx, id)` 列出 ZIP 文件中的条目（服务端只读取文件末尾的目录，不下载整个文件，GBK 文件名自动转换），`OpenZipEntry(ctx, id, entry.Index)` 下载其中一个文件的解压内容（计入下载流量）。

保留锁定（合规保留 / 法律保留）：`SetRetention(ctx, id, fileapi.RetentionOptions{RetainUntil: &t})` 设置或延长保留期限，`LegalHold: &hold` 设置或解除法律保留；锁定期内（`File.Locked`）删除、上传新版本、恢复或删除历史版本返回 `fileapi.CodeFileLocked`（42302），`ListQuery.Locked` 按是否锁定筛选。客户端只能延长保留期限，缩短或清除需要管理员操作；客户端配置了 `retention_days` 时上传的文件自动锁定。

Office 文档预览：上传的 Word / Excel / PPT 由服务端转为 PDF（系统设置 `preview.auto`，或 `RequestPreview` 按需生成），`WaitPreview` 等待完成，`File.Preview` 为状态；`AccessURL(ctx, id, &fileapi.URLOptions{Preview: true})` 签发预览 PDF 的地址，浏览器内直接打开。

### 分享链接

```go
s, err := client.ShareFiles(ctx, []int64{f.ID}, &fileapi.ShareOptions{Password: "Ab12", ExpiresIn: 7 * 24 * time.Hour, MaxDownloads: 10})
// s.URL 为 FileApi 自带的分享页（{base}/s/{code}）：文件列表、在线预览、下载与打包下载；访问者输入提取码后可见
s, err = client.ShareFolder(ctx, "/合同/2026/", true, &fileapi.ShareOptions{PreviewOnly: true}) // 分享目录（访问时实时读取），只能预览
page, err := client.Shares(ctx, 1, 20)                                                          // 下载与浏览次数在 Downloads / Views 中
err = client.DeleteShare(ctx, s.Code)                                                           // 取消分享，立即失效
```

自建分享页时可用 `GET {s.URL}/info`（JSON，提取码放在 `X-Link-Password` 请求头）取得文件列表，文件地址为 `{s.URL}/f/{id}`（`?download=1` 下载）。

### 文件收集

```go
col, err := client.CreateCollection(ctx, "第 3 周作业", &fileapi.CollectionOptions{
	TagIDs: []int64{tag.ID}, Extensions: []string{"pdf"}, MaxFiles: 100, GroupByUploader: true, ExpiresIn: 7 * 24 * time.Hour,
})
// col.URL 为 FileApi 自带的收集页（{base}/c/{code}）：外部人员无需登录即可上传，文件放入 col.Folder 并打上标签
files, err := client.CollectionFiles(ctx, col.Code, nil) // 收到的文件（元数据 uploader 为提交者姓名）
off := false
client.UpdateCollection(ctx, col.Code, fileapi.CollectionUpdate{Enabled: &off}) // 关闭收集
```

每次提交推送 `EventCollectionSubmitted`，用 `e.CollectionSubmission()` 解析（收集码、姓名、备注、上传成功的文件与失败数）；`Collection.Submissions` 为提交次数。

### 批量打包下载

`CreateArchive` 签发多个文件的 ZIP 下载链接：访问时服务端边读边写流式返回，包内路径可含目录、同名自动加序号，有效期内可多次下载，按实际字节计入下载流量。

```go
arch, err := client.CreateArchive(ctx, []fileapi.ArchiveFile{{ID: 1, Name: "合同/甲方.pdf"}, {ID: 2}},
	&fileapi.ArchiveOptions{Filename: "合同资料", ExpiresIn: time.Hour})
// arch.URL 交给浏览器下载
```

### 限次 / 可吊销链接

`URLOptions.MaxVisits` 限定访问次数、`Revocable` 签发可吊销的链接（仅统一入口链接，须有有效期），返回的 `LinkID` 用于查询与吊销。
完整下载或从头开始的分段请求计一次，视频拖动、断点续传与 HEAD 不计；用完返回 410 / `CodeLinkExhausted`，吊销后返回 410 / `CodeLinkRevoked`。

```go
u, err := client.AccessURL(ctx, id, &fileapi.URLOptions{Expires: 24 * time.Hour, MaxVisits: 3})
link, err := client.LinkStatus(ctx, u.LinkID) // link.Visits / link.MaxVisits
err = client.RevokeLink(ctx, u.LinkID)        // 立即失效
```

`URLOptions.Password` 设置提取码（4 ~ 16 位字母或数字，不区分大小写）：浏览器打开链接时先输入提取码，程序访问在 `fileapi.LinkPasswordHeader`（`X-Link-Password`）请求头中携带；
返回的 `AccessURL.PasswordProtected` 为 true。缺少 / 错误 / 错误次数过多分别为 `CodeLinkPasswordRequired` / `CodeLinkPasswordWrong` / `CodeLinkPasswordLocked`。

### 文件版本历史

上传时设置 `UploadOptions.VersionOf` / `DirectUploadOptions.VersionOf`（文件 ID）即作为该文件的新版本：文件 ID、业务对象、元数据、过期时间与已签发的链接不变，
旧内容保留为历史版本，返回文件的最新信息（`File.Version` 加 1）。每个文件保留的历史版本数按客户端设置（默认 10 个），超出时删除最旧的；历史版本字节计入配额，
文件彻底删除时一并删除。新版本不参与秒传与去重。

```go
f, err := client.Upload(ctx, "合同-修订.pdf", r, size, &fileapi.UploadOptions{VersionOf: id})
versions, err := client.ListVersions(ctx, id)               // 版本号大的在前
obj, err := client.OpenVersion(ctx, id, versions[0].ID)     // 下载历史版本
f, err = client.RestoreVersion(ctx, id, versions[0].ID)     // 恢复为当前内容（当前内容转为历史版本）
err = client.DeleteVersion(ctx, id, versions[0].ID)
```

### 媒体信息

文件信息带 `Width` / `Height` / `DurationMS`：图片（按 EXIF 方向校正后）与视频的宽高、音视频时长（毫秒），未知时为空。生成了缩略图的图片上传时即返回，
其余由服务端在上传后补齐（音视频需服务端配置 ffprobe）。按媒体信息筛选用 `ListQuery` 的 `MinWidth` / `MaxWidth` / `MinHeight` / `MaxHeight` / `MinDurationMS` / `MaxDurationMS`（0 表示不限）。

### 回收站

服务端默认开启回收站（系统设置 `trash.retention_days`，客户端可单独设置）：`Delete` / `BatchDelete` 把文件移入回收站，
保留期内查询、下载、访问链接都如同已删除，也不占用配额；`Restore` 恢复后之前签发且未过期的链接重新可用。

```go
_ = client.Delete(ctx, id)                       // 移入回收站（服务端关闭回收站时直接彻底删除）
page, _ := client.ListTrash(ctx, &fileapi.ListQuery{ObjectType: "avatar"}) // 文件带 TrashedAt、PurgeAt
f, err := client.Restore(ctx, id)                // 超出配额时返回 CodeQuotaExceeded
_ = client.DeletePermanently(ctx, id)            // 不进回收站；也用于彻底删除回收站中的文件
n, _ := client.EmptyTrash(ctx)                   // 清空回收站（服务端在几分钟内彻底删除）
```

Webhook 事件：移入回收站触发 `file.deleted`（`WebhookFile.Trashed` 为 true），恢复触发 `file.restored`，彻底删除触发 `file.purged`。

### 错误处理

服务端错误为 `*fileapi.Error`（HTTP 状态码、业务码、消息、request_id）：

```go
f, err := c.Get(ctx, id)
switch {
case fileapi.IsNotFound(err):
	// 不存在、已删除或属于其它客户端
case fileapi.IsCode(err, fileapi.CodeQuotaExceeded):
	// 配额不足
case err != nil:
	var e *fileapi.Error
	if errors.As(err, &e) {
		log.Printf("fileapi %d %s request_id=%s", e.Code, e.Message, e.RequestID)
	}
}
```

直传时存储平台拒绝上传返回 `*fileapi.StorageError`。

服务端开启病毒扫描时：代理上传发现病毒返回 `CodeFileInfected`（42201，消息带病毒名）；直传的文件确认后 `File.ScanStatus` 为 `pending`，
后台扫描发现病毒的文件会被隔离（之后查询返回不存在），可订阅 Webhook `file.quarantined`。服务端开启 `scan.block_pending` 时，
扫描完成前下载、签发直链返回 `CodeScanPending`（42301）。

`c.Usage(ctx)` 查询存储用量与本月流量（下载流量 `Traffic.EgressBytes` = 代理返回的实际字节 + 跳转与直链按对象大小估算的字节）。
客户端设置了月流量配额时，达到后下载、统一入口访问与签发直链返回 `CodeTrafficExceeded`（42903），下月 1 日恢复。
设置了月转码分钟配额（`Usage.TranscodeQuotaMinutes`，本月已用 `Usage.TranscodeUsedMS`）时，用完后 `Transcode` 返回 `CodeTranscodeExceeded`（42905），剩余不足本次所需（源时长 × 档位数）时转码任务失败。

### 重试与时钟

- 查询类请求（GET、批量查询、确认上传等幂等请求）在网络错误与 429/502/503/504 时重试，默认 2 次，指数退避并遵循 `Retry-After`；每次重试重新签名。`WithRetries(0)` 关闭。上传与删除不自动重试。
- 本机时钟与服务端偏差超过 5 分钟时，服务端返回 40103，SDK 按响应中的服务端时间校准后立即重试一次，此后的请求都使用校准后的时间。

### 选项

```go
fileapi.New(baseURL, id, secret,
	fileapi.WithHTTPClient(&http.Client{Transport: myTransport}), // 代理、TLS、连接池；不要设置整体 Timeout，用 ctx 控制
	fileapi.WithRetries(3),
	fileapi.WithUserAgent("order-service/1.4"),
)
```

## 前端上传令牌

浏览器、小程序直接上传时，不要把客户端密钥交给前端：业务后端用 `NewUploadToken` 在本地签发短时令牌（不请求 FileApi），
前端在请求头 `X-Upload-Token`（`fileapi.HeaderUploadToken`）中携带令牌调用 `POST {baseURL}/upload/v1/files`（multipart，文件字段 `file`）。

```go
tok, err := client.NewUploadToken(&fileapi.UploadTokenOptions{
	ExpiresIn:    10 * time.Minute, // 默认 15 分钟，最长 7 天
	MaxSize:      5 << 20,          // 不超过客户端自身的上限
	ContentTypes: []string{"image/*"},
	ObjectType:   "user_avatar", ObjectID: userID, ReplaceExisting: true, // 上传方不能修改
	MaxUploads:   1,                // 默认 1
})
// 把 tok.Token、tok.ExpiresAt 返回给前端
```

前端推荐使用浏览器 / 小程序 SDK（FileApi 源码仓库的 `sdk/browser` 目录）：小文件经 FileApi 代理上传，大文件自动分片直传到存储，带进度、取消与断点续传。上传完成照常触发 `file.uploaded` Webhook。
设置 `VersionOf`（文件 ID）签发只能上传该文件新版本的令牌：前端凭它上传的文件作为新版本（文件 ID 与链接不变，旧内容保留为历史版本），不能与业务对象、元数据、`ReplaceExisting`、`FileExpiresIn` 同时使用。

令牌格式、限制与前端示例见服务端 README 的"前端上传令牌"。

## 接收 Webhook

FileApi 推送事件时带签名头 `X-FileApi-Signature`，用 `VerifyWebhook` 校验（防伪造、防重放），或直接用现成的 `http.Handler`：

```go
http.Handle("/fileapi/events", fileapi.WebhookHandler(os.Getenv("FILEAPI_WEBHOOK_SECRET"),
	func(r *http.Request, e *fileapi.WebhookEvent) error {
		switch e.Type {
		case fileapi.EventFileUploaded:
			f, err := e.File()
			if err != nil {
				return err
			}
			return onUploaded(r.Context(), e.ID, f) // 按事件 ID 去重：同一事件可能重复推送
		}
		return nil // 返回错误时响应 500，FileApi 按退避重试
	}))
```

完整示例见 [examples/webhook-receiver](examples/webhook-receiver/main.go)。

用量告警：设置了配额的客户端存储用量 / 本月下载流量达到预警比例（服务端系统设置，默认 80%）与配额时各推送一次 `EventQuotaStorage` / `EventQuotaTraffic` / `EventQuotaTranscode`（转码数据为 `UsedMinutes` / `QuotaMinutes`），
用 `e.UsageAlert()` 解析（`Level` 为 `UsageWarning` / `UsageExceeded`，含用量、配额与百分比）；当前级别也在 `Usage(ctx)` 的 `Alerts` 中返回。

## 测试

```bash
go test -race ./...                                              # 单元测试（模拟服务端）
FILEAPI_ADMIN_API_KEY=... go test -tags=integration ./...        # 对运行中的 FileApi 做端到端测试
```

集成测试用管理接口创建临时客户端，覆盖全部能力后删除其文件与客户端。可选环境变量：`FILEAPI_BASE_URL`（默认 `http://127.0.0.1:8080`）、`FILEAPI_TEST_STORAGE_ID`（绑定指定存储）。
