package fileapi_test

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/apersonw/fileapi-go/fileapi"
)

func Example() {
	c, err := fileapi.New("https://files.example.com", os.Getenv("FILEAPI_CLIENT_ID"), os.Getenv("FILEAPI_CLIENT_SECRET"))
	if err != nil {
		log.Fatal(err)
	}
	ctx := context.Background()

	// 小文件代理上传，关联业务对象
	res, err := c.UploadFile(ctx, "./avatar.png", &fileapi.UploadOptions{ObjectType: "user_avatar", ObjectID: "42"})
	if err != nil {
		log.Fatal(err)
	}
	// 签发 1 小时有效的统一入口链接
	link, err := c.AccessURL(ctx, res.ID, &fileapi.URLOptions{Expires: time.Hour})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(link.URL)
}

func ExampleClient_UploadFileDirect() {
	c, _ := fileapi.New("https://files.example.com", "fa_xxx", "secret")
	// 大文件直传存储平台：超过 64MiB 自动分片，4 个分片并发
	f, err := c.UploadFileDirect(context.Background(), "./backup.tar.gz", &fileapi.DirectUploadOptions{
		Progress: func(sent, total int64) { fmt.Printf("\r%d%%", sent*100/total) },
	})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(f.ID, f.FormatSize)
}

func ExampleClient_ListAll() {
	c, _ := fileapi.New("https://files.example.com", "fa_xxx", "secret")
	for f, err := range c.ListAll(context.Background(), &fileapi.ListQuery{Category: fileapi.CategoryImage}) {
		if err != nil {
			log.Fatal(err)
		}
		fmt.Println(f.ID, f.OriginalFilename)
	}
}

func ExampleIsNotFound() {
	c, _ := fileapi.New("https://files.example.com", "fa_xxx", "secret")
	if _, err := c.Get(context.Background(), 1); fileapi.IsNotFound(err) {
		fmt.Println("文件不存在或已删除")
	}
}
