package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"sync"
	"time"

	"github.com/cheggaaa/pb"
)

type Worker struct {
	Url       string
	File      *os.File
	Count     int64
	SyncWG    sync.WaitGroup
	TotalSize int64
	Progress
}

type Progress struct {
	Pool *pb.Pool
	Bars []*pb.ProgressBar
}

func main() {
	var t = flag.Bool("t", false, "file name with datetime")
	var worker_count = flag.Int64("c", 5, "connection count")
	flag.Parse()

	var download_url string
	fmt.Print("Please enter a URL: ")
	fmt.Scanf("%s", &download_url)

	// Get header from the url
	log.Println("Url:", download_url)
	file_size, err := getSizeAndCheckRangeSupport(download_url)
	log.Printf("File size: %d bytes\n", file_size)

	var file_path string
	if *t {
		file_path = filepath.Dir(os.Args[0]) + string(filepath.Separator) + strconv.FormatInt(time.Now().UnixNano(), 10) + "_" + getFileName(download_url)
	} else {
		file_path = filepath.Dir(os.Args[0]) + string(filepath.Separator) + getFileName(download_url)
	}
	log.Printf("Local path: %s\n", file_path)
	f, err := os.OpenFile(file_path, os.O_APPEND|os.O_CREATE|os.O_RDWR, 0666)
	handleError(err)
	defer f.Close()

	// New worker struct for downloading file
	var worker = Worker{
		Url:       download_url,
		File:      f,
		Count:     *worker_count,
		TotalSize: file_size,
	}

	var start, end int64
	var partial_size = int64(file_size / *worker_count)
	now := time.Now().UTC()
	for num := int64(0); num < worker.Count; num++ {
		// New sub progress bar
		bar := pb.New(100).Prefix(fmt.Sprintf("Part %d ", num))
		bar.ShowTimeLeft = false
		bar.ShowPercent = false
		worker.Progress.Bars = append(worker.Progress.Bars, bar)

		if num == worker.Count {
			end = file_size // last part
		} else {
			end = start + partial_size
		}

		worker.SyncWG.Add(1)
		go worker.writeRange(num, start, end-1)
		start = end
	}
	worker.Progress.Pool, err = pb.StartPool(worker.Progress.Bars...)
	handleError(err)
	worker.SyncWG.Wait()
	worker.Progress.Pool.Stop()
	log.Println("Elapsed time:", time.Since(now))
	log.Println("Done!")
	blockForWindows()
}

func (w *Worker) writeRange(part_num int64, start int64, end int64) {
	var written int64
	body, size, err := w.getRangeBody(start, end)
	if err != nil {
		log.Fatalf("Part %d request error: %s\n", part_num, err.Error())
	}
	defer body.Close()
	defer w.Bars[part_num].Finish()
	defer w.SyncWG.Done()

	percent_flag := map[int64]bool{}
	buf := make([]byte, 32*1024) // make a buffer to keep chunks that are read
	for {
		nr, er := body.Read(buf)
		if nr > 0 {
			nw, err := w.File.WriteAt(buf[0:nr], start)
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

			// Set current percent to progress bar
			p := int64(float32(written) / float32(size) * 100)
			_, flagged := percent_flag[p]
			if !flagged {
				percent_flag[p] = true
				w.Bars[int(part_num)].Set64(p) // Set current percent number
			}
		}
		if er != nil {
			if er.Error() == "EOF" {
				if size == written {
					// Downloading successfully
				} else {
					handleError(errors.New(fmt.Sprintf("Part %d unfinished.\n", part_num)))
				}
				break
			}
			handleError(errors.New(fmt.Sprintf("Part %d occured error: %s\n", part_num, er.Error())))
		}
	}
}

func (w *Worker) getRangeBody(start int64, end int64) (io.ReadCloser, int64, error) {
	var client http.Client
	req, err := http.NewRequest("GET", w.Url, nil)
	// req.Header.Set("cookie", "")
	// log.Printf("Request header: %s\n", req.Header)
	if err != nil {
		return nil, 0, err
	}

	// Set range header
	req.Header.Add("Range", fmt.Sprintf("bytes=%d-%d", start, end))
	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	size, err := strconv.ParseInt(resp.Header["Content-Length"][0], 10, 64)
	return resp.Body, size, err
}

func getSizeAndCheckRangeSupport(url string) (size int64, err error) {
	client := &http.Client{}
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return
	}
	// req.Header.Set("cookie", "")
	// log.Printf("Request header: %s\n", req.Header)
	res, err := client.Do(req)
	if err != nil {
		return
	}
	log.Printf("Response header: %v\n", res.Header)
	header := res.Header
	accept_ranges, supported := header["Accept-Ranges"]
	if !supported {
		return 0, errors.New("Doesn't support `Accept-Ranges`.")
	} else if supported && accept_ranges[0] != "bytes" {
		return 0, errors.New("Support `Accept-Ranges`, but value is not `bytes`.")
	}
	size, err = strconv.ParseInt(header["Content-Length"][0], 10, 64)
	return
}

func getFileName(download_url string) string {
	url_struct, err := url.Parse(download_url)
	handleError(err)
	return filepath.Base(url_struct.Path)
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
