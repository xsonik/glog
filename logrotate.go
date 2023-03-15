package glog

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

type DateRotator struct {
	timeDiffToUTC     int64
	lastTime          int64
	period            int64
	maxAge            int64
	logPath           string
	filename          string
	mutex             sync.RWMutex
	outFile           *os.File
	logFileTimeFormat string
	ext               string
}

func NewDateRotator(directory, format, ext string, maxAge int64) (*DateRotator, error) {
	absolutePath, err := os.Executable()
	if err != nil {
		return nil, err
	}
	dir, _ := filepath.Split(absolutePath)
	logPath := filepath.Join(dir, directory)
	_, err = os.Stat(logPath)
	if err != nil && os.IsNotExist(err) {
		err = os.MkdirAll(logPath, os.ModePerm)
		if err != nil {
			return nil, err
		}
	}

	var tw DateRotator
	tw.logPath = logPath
	tw.logFileTimeFormat = format
	tw.ext = ext
	tw.maxAge = maxAge
	// 时区偏移
	_, offset := time.Now().Zone()
	tw.timeDiffToUTC = (time.Duration(offset) * time.Second).Nanoseconds()
	tw.period = (24 * time.Hour).Nanoseconds()
	return &tw, nil
}

func (tw *DateRotator) Write(p []byte) (n int, err error) {
	tw.mutex.Lock()
	defer tw.mutex.Unlock()

	fh, err := tw.getFileHandler()
	if err != nil {
		return 0, err
	}

	if fh == nil {
		return 0, errors.New(`target io.Writer is closed`)
	}

	return fh.Write(p)
}

func (tw *DateRotator) getFileHandler() (io.Writer, error) {
	nowUnixNano := time.Now().UnixNano()
	current := nowUnixNano - ((nowUnixNano + tw.timeDiffToUTC) % tw.period)
	if (current - tw.lastTime) < tw.period {
		return tw.outFile, nil
	}

	logfile := time.Unix(0, current).Format(tw.logFileTimeFormat) + tw.ext
	if tw.filename == logfile {
		return tw.outFile, nil
	}

	if tw.outFile != nil {
		err := tw.outFile.Close()
		if err != nil {
			return nil, err
		}
	}

	filename := filepath.Join(tw.logPath, logfile)
	fh, err := os.OpenFile(filename, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return nil, errors.New(fmt.Sprintf("failed to open file %s: %s", filename, err))
	}

	tw.outFile = fh
	tw.filename = logfile
	tw.lastTime = current

	go func() {
		_ = tw.cleanRunOnce()
	}()

	return fh, nil
}

func (tw *DateRotator) Close() error {
	tw.mutex.Lock()
	defer tw.mutex.Unlock()

	if tw.outFile != nil {
		err := tw.outFile.Close()
		if err != nil {
			return err
		}
		tw.outFile = nil
	}
	return nil
}

func (tw *DateRotator) cleanRunOnce() error {
	if tw.maxAge == 0 {
		return nil
	}

	files, err := tw.oldLogFiles()
	if err != nil {
		return err
	}

	var remove []logInfo

	if tw.maxAge > 0 {
		diff := time.Duration(int64(24*time.Hour) * tw.maxAge)
		cutoff := time.Now().Local().Add(-diff)
		for _, f := range files {
			if f.timestamp.Before(cutoff) {
				remove = append(remove, f)
			}
		}
	}

	for _, f := range remove {
		errRemove := os.Remove(filepath.Join(tw.logPath, f.Name()))
		if err == nil && errRemove != nil {
			err = errRemove
		}
	}

	return err
}

// oldLogFiles returns the list of log files, sorted by ModTime
func (tw *DateRotator) oldLogFiles() ([]logInfo, error) {
	dirEntries, err := os.ReadDir(tw.logPath)
	if err != nil {
		return nil, fmt.Errorf("can't read log file directory: %s", err)
	}

	var logFiles []logInfo
	for _, entry := range dirEntries {
		if entry.IsDir() {
			continue
		}
		if t, err := tw.timeFromName(entry.Name()); err == nil {
			if f, err := entry.Info(); err == nil {
				logFiles = append(logFiles, logInfo{t, f})
			}
			continue
		}
	}

	sort.Sort(byFormatTime(logFiles))

	return logFiles, nil
}

func (tw *DateRotator) timeFromName(filename string) (time.Time, error) {
	if !strings.HasSuffix(filename, tw.ext) {
		return time.Time{}, errors.New("mismatched extension")
	}
	ts := filename[0 : len(filename)-len(tw.ext)]
	return time.Parse(tw.logFileTimeFormat, ts)
}

type logInfo struct {
	timestamp time.Time
	os.FileInfo
}

type byFormatTime []logInfo

func (b byFormatTime) Len() int {
	return len(b)
}

func (b byFormatTime) Less(i, j int) bool {
	return b[i].timestamp.After(b[j].timestamp)
}

func (b byFormatTime) Swap(i, j int) {
	b[i], b[j] = b[j], b[i]
}
