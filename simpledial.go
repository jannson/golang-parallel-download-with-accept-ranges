package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"sync/atomic"
	"time"
)

type connInfo struct {
	readTotal  uint64
	writeTotal uint64
}

type stasticInfo struct {
	sinfo               connInfo
	readTotal           uint64
	writeTotal          uint64
	lastTime            time.Time
	downloadSecondBytes int
	uploadSecondBytes   int
}

type sConn struct {
	net.Conn
	sinfo *connInfo
}

func (c *sConn) Read(b []byte) (n int, err error) {
	n, err = c.Conn.Read(b)
	atomic.AddUint64(&c.sinfo.readTotal, uint64(n))
	return
}

func (c *sConn) Write(b []byte) (n int, err error) {
	n, err = c.Conn.Write(b)
	atomic.AddUint64(&c.sinfo.writeTotal, uint64(n))
	return
}

func (si *stasticInfo) updateSpeed() {
	readTotal := atomic.LoadUint64(&si.sinfo.readTotal)
	writeTotal := atomic.LoadUint64(&si.sinfo.writeTotal)

	now := time.Now()
	mil := uint64(now.Sub(si.lastTime).Nanoseconds()/1000000) + 1
	speed1 := (readTotal - si.readTotal) * 1000 / mil
	speed2 := (writeTotal - si.writeTotal) * 1000 / mil

	if si.downloadSecondBytes == 0 {
		si.downloadSecondBytes = int(speed1)
	} else {
		si.downloadSecondBytes = (int(speed1) + si.downloadSecondBytes) / 2
	}

	if si.uploadSecondBytes == 0 {
		si.uploadSecondBytes = int(speed2)
	} else {
		si.uploadSecondBytes = (int(speed2) + si.uploadSecondBytes) / 2
	}
}

var defaultDialer = &net.Dialer{Timeout: 150 * time.Second, KeepAlive: 30 * time.Second}

func main() {
	si := stasticInfo{lastTime: time.Now()}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ticker := time.NewTicker(time.Second * 3)
	defer ticker.Stop()
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				si.updateSpeed()
				log.Println(si.downloadSecondBytes / 1024 / 1024)
			}
		}
	}()

	client := http.Client{
		Transport: &http.Transport{
			Dial: func(network, addr string) (net.Conn, error) {
				conn, err := defaultDialer.Dial(network, addr)
				if err != nil {
					return nil, err
				}
				return &sConn{conn, &si.sinfo}, nil
			},
		},
	}
	url := "http://127.0.0.1:8899/files/HomeLinux-8B46EC49E550/a.tar.gz"
	req, err := http.NewRequest("GET", url, nil)
	req = req.WithContext(ctx)

	// req.Header.Set("cookie", "")
	// log.Printf("Request header: %s\n", req.Header)
	if err != nil {
		return
	}

	// Set range header
	//req.Header.Add("Range", fmt.Sprintf("bytes=%d-%d", start, end))
	resp, err := client.Do(req)
	if err != nil {
		return
	}
	size, err := strconv.ParseInt(resp.Header["Content-Length"][0], 10, 64)
	body := resp.Body

	file_path := filepath.Dir(os.Args[0]) + string(filepath.Separator) + strconv.FormatInt(time.Now().UnixNano(), 10) + "_" + getFileName(url)
	f, err := os.OpenFile(file_path, os.O_APPEND|os.O_CREATE|os.O_RDWR, 0666)
	if err != nil {
		return
	}
	defer f.Close()
	var start int64 = 0
	part_num := 0
	var written int64

	// make a buffer to keep chunks that are read
	buf := make([]byte, 4*1024)
	for {
		nr, er := body.Read(buf)
		if nr > 0 {
			nw, err := f.WriteAt(buf[0:nr], start)
			if err != nil {
				log.Fatalf("Part %d occured error: %s.\n", part_num, err.Error())
			}
			if nr != nw {
				log.Fatalf("Part %d occured error of short writiing.\n", part_num)
			}

			start = int64(nw) + start
			if nw > 0 {
				written += int64(nw)
			}
		}
		if er != nil {
			if er.Error() == "EOF" {
				if size == written {
					// Download successfully
				} else {
					handleError(errors.New(fmt.Sprintf("Part %d unfinished.\n", part_num)))
				}
				break
			}
			handleError(errors.New(fmt.Sprintf("Part %d occured error: %s\n", part_num, er.Error())))
		}
	}
}

func handleError(err error) {
	if err != nil {
		log.Println("err:", err)
		blockForWindows()
		os.Exit(1)
	}
}

func blockForWindows() { // Prevent windows from closing exe window.
	if runtime.GOOS == "windows" {
		for {
			log.Println("[Press `Ctrl+C` key to exit...]")
			time.Sleep(10 * time.Second)
		}
	}
}

func getFileName(download_url string) string {
	url_struct, err := url.Parse(download_url)
	handleError(err)
	return filepath.Base(url_struct.Path)
}
