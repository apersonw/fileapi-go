// webhook-receiver 演示如何接收 FileApi 的 Webhook 推送：校验签名后按事件类型处理，并按事件 ID 去重。
//
//	FILEAPI_WEBHOOK_SECRET=whsec_xxx go run ./examples/webhook-receiver -addr :8099
//
// 每个通过校验的事件以一行 JSON 写到标准输出。
package main

import (
	"encoding/json"
	"flag"
	"log"
	"net/http"
	"os"
	"sync"

	"github.com/apersonw/fileapi-go/fileapi"
)

func main() {
	addr := flag.String("addr", ":8099", "监听地址")
	path := flag.String("path", "/hook", "接收路径")
	flag.Parse()
	secret := os.Getenv("FILEAPI_WEBHOOK_SECRET")
	if secret == "" {
		log.Fatal("FILEAPI_WEBHOOK_SECRET is required")
	}

	var mu sync.Mutex
	seen := map[string]bool{} // 至少投递一次：同一事件可能重复推送，按 ID 去重（生产环境应持久化）
	out := json.NewEncoder(os.Stdout)
	http.Handle(*path, fileapi.WebhookHandler(secret, func(r *http.Request, e *fileapi.WebhookEvent) error {
		mu.Lock()
		defer mu.Unlock()
		if seen[e.ID] {
			return nil
		}
		seen[e.ID] = true
		switch e.Type {
		case fileapi.EventFileUploaded, fileapi.EventFileDeleted:
			f, err := e.File()
			if err != nil {
				return err
			}
			log.Printf("%s: file %d %s (%d bytes)", e.Type, f.ID, f.OriginalFilename, f.Size)
		default:
			log.Printf("%s: %s", e.Type, e.Data)
		}
		return out.Encode(e)
	}))
	log.Printf("listening on %s%s", *addr, *path)
	log.Fatal(http.ListenAndServe(*addr, nil))
}
