package fstools_test

import (
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dustin/go-humanize"

	"github.com/Avalanche-io/c4"

	"github.com/absfs/absfs"
	"github.com/absfs/basefs"
	"github.com/absfs/fstools"
	"github.com/absfs/ioutil"
	"github.com/absfs/memfs"
	"github.com/absfs/osfs"
)

type fileinfo struct {
	Digest     c4.Digest   // 64
	Size       int64       // 8
	TotalSize  int64       // 8
	Mode       os.FileMode // 4
	Children   int32       // 4
	ModTime    time.Time   // 15
	LatestTime time.Time   // 15
	Name       string      // ?
}

func (i *fileinfo) MarshalBinary() (data []byte, err error) {
	data = make([]byte, 118+len(i.Name))
	copy(data[0:64], i.Digest)
	binary.LittleEndian.PutUint64(data[64:72], uint64(i.Size))
	binary.LittleEndian.PutUint64(data[72:80], uint64(i.TotalSize))
	binary.LittleEndian.PutUint32(data[80:84], uint32(i.Mode))
	binary.LittleEndian.PutUint32(data[84:88], uint32(i.Children))
	timedata, err := i.ModTime.MarshalBinary()
	if err != nil {
		return nil, err
	}
	copy(data[88:103], timedata)

	timedata, err = i.LatestTime.MarshalBinary()
	if err != nil {
		return nil, err
	}
	copy(data[103:118], timedata)

	copy(data[118:], []byte(i.Name))
	return data, nil
}

func (i *fileinfo) UnmarshalBinary(data []byte) error {
	copy(i.Digest, data[0:64])
	i.Size = int64(binary.LittleEndian.Uint64(data[64:72]))
	i.TotalSize = int64(binary.LittleEndian.Uint64(data[72:80]))
	i.Mode = os.FileMode(binary.LittleEndian.Uint32(data[80:84]))
	i.Children = int32(binary.LittleEndian.Uint32(data[84:88]))
	i.ModTime.UnmarshalBinary(data[88:103])
	i.LatestTime.UnmarshalBinary(data[103:118])
	i.Name = string(data[118:])
	return nil
}

func TestTime(t *testing.T) {
	var fs, localfs absfs.FileSystem
	var err error
	localfs, err = osfs.NewFS()
	if err != nil {
		t.Fatal(err)
	}
	path, err := filepath.Abs("../..") // "/xx/d/go/src/github.com"
	if err != nil {
		t.Fatal(err)
	}
	fmt.Printf("loading path %q\n", path)
	localfs, err = basefs.NewFS(localfs, path)
	if err != nil {
		t.Fatal(err)
	}

	fs, err = memfs.NewFS()
	if err != nil {
		t.Fatal(err)
	}

	start_time := time.Now()
	cnt := 0
	err = fstools.Walk(localfs, "/", func(path string, info os.FileInfo, err error) error {
		cnt++

		if info.IsDir() && path != "/" {
			err = fs.Mkdir(path, info.Mode())
			if err != nil {
				return err
			}
		}
		metadata := &fileinfo{
			Size:    info.Size(),
			ModTime: info.ModTime(),
			Mode:    info.Mode(),
			Name:    info.Name(),
		}
		return SaveMetadata(fs, path, metadata)
	})
	if err != nil {
		t.Fatal(err)
	}
	fmt.Printf("local walk: %s\n", time.Now().Sub(start_time))
	start_time = time.Now()
	fmt.Printf("cnt: %d\n", cnt)
	err = fstools.PostOrder(fs, nil, "/", func(path string, info os.FileInfo, err error) error {
		metadata, err := LoadMetadata(fs, path)
		if err != nil {
			fmt.Printf("call error %q: %s\n", path, err)
			return err
		}

		err = fs.Chtimes(path, time.Now(), metadata.ModTime)
		if err != nil {
			fmt.Printf("error %q: %s\n", path, err)
			return err
		}

		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	fmt.Printf("local PostOrder for Chtimes: %s\n", time.Now().Sub(start_time))

	f, err := fs.Open("/")
	if err != nil {
		t.Fatal(err)
	}
	infos, err := f.Readdir(-1)
	if err != nil {
		t.Fatal(err)
	}
	f.Close()

	path = "/"
	for _, info := range infos {
		if info.Name() == "." || info.Name() == ".." {
			continue
		}

		metadata, err := LoadMetadata(fs, filepath.Join(path, info.Name()))
		if err != nil {
			t.Fatal(err)
		}

		fmt.Printf("%s  %q%*s% 8d %s\n", metadata.Mode, metadata.Name, 20-len(metadata.Name), " ", metadata.Size, metadata.ModTime.Format(time.UnixDate))
	}

	count := new(intStack)
	size := new(intStack)
	latest := new(timeStack)
	var depth int
	depth = 0
	cwd := ""
	err = fstools.PostOrder(fs, nil, "/", func(path string, info os.FileInfo, err error) error {
		metadata, err := LoadMetadata(fs, path)
		if err != nil {
			fmt.Printf("erroring on %q, %s\n", path, err)
			return err
		}

		dir := filepath.Dir(path)
		for !strings.HasPrefix(dir, cwd) {
			count.add(count.pop())
			size.add(size.pop())
			latest.add(latest.pop())
			cwd = filepath.Dir(cwd)
		}

		cwd = dir
		paths := strings.Split(cwd, "/")
		d := len(paths)
		if cwd == "/" {
			d = 1
		}
		if depth < len(paths) {
			for j, parent := range paths[depth:] {
				if parent == "" && j == 0 {
					continue
				}
				count.push(0)
				size.push(0)
				latest.push(time.Time{})
			}
		}
		count.add(1)
		size.add(metadata.Size)
		latest.add(metadata.ModTime)
		depth = d

		if info.IsDir() {
			metadata.Children = int32(count.Peek())
			metadata.TotalSize = size.Peek()
			metadata.LatestTime = latest.Peek()
		}

		return SaveMetadata(fs, path, metadata)
	})
	if err != nil {
		t.Fatal(err)
	}

	metadata, err := LoadMetadata(fs, "/")
	if err != nil {
		t.Fatal(err)
	}

	fmt.Printf("totals: children, size: %s, %s\n", humanize.Comma(int64(metadata.Children)), humanize.Bytes(uint64(metadata.TotalSize)))

}

type intStack []int64

func (s *intStack) push(size int64) {
	(*s) = append((*s), size)
}

func (s *intStack) pop() (size int64) {
	if len(*s) == 0 {
		return 0
	}
	size = (*s)[len(*s)-1]
	(*s) = (*s)[:len(*s)-1]
	return size
}

func (s *intStack) Peek() (size int64) {
	if len(*s) == 0 {
		return 0
	}
	return (*s)[len(*s)-1]
}

func (s *intStack) empty() bool {
	return len(*s) == 0
}

func (s *intStack) add(size int64) int64 {
	(*s)[len(*s)-1] += size
	return (*s)[len(*s)-1]
}

type timeStack []time.Time

func (s *timeStack) push(t time.Time) {
	(*s) = append((*s), t)
}

func (s *timeStack) pop() (t time.Time) {
	if len(*s) == 0 {
		return time.Time{}
	}
	t = (*s)[len(*s)-1]
	(*s) = (*s)[:len(*s)-1]
	return t
}

func (s *timeStack) Peek() (size time.Time) {
	if len(*s) == 0 {
		return time.Time{}
	}
	return (*s)[len(*s)-1]
}

func (s *timeStack) empty() bool {
	return len(*s) == 0
}

func (s *timeStack) add(t time.Time) time.Time {
	if t.After((*s)[len(*s)-1]) {
		(*s)[len(*s)-1] = t
	}
	return (*s)[len(*s)-1]
}

func SaveMetadata(fs absfs.Filer, path string, metadata *fileinfo) error {
	var f absfs.File
	var err error

	if metadata.Mode&os.ModeDir != 0 {
		f, err = fs.OpenFile(path, os.O_RDWR, metadata.Mode)
	} else {
		f, err = fs.OpenFile(path, os.O_CREATE|os.O_RDWR|os.O_TRUNC, metadata.Mode)
	}
	if err != nil {
		return err
	}
	defer f.Close()
	data, err := metadata.MarshalBinary()
	if err != nil {
		return err
	}
	_, err = f.Write(data)
	if err != nil {
		return err
	}
	return nil
	// return gob.NewEncoder(f).Encode(metadata)
}

func LoadMetadata(fs absfs.Filer, path string) (*fileinfo, error) {
	f, err := fs.OpenFile(path, os.O_RDONLY, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	data, err := ioutil.ReadAll(f)
	if err != nil {
		return nil, err
	}
	metadata := new(fileinfo)
	err = metadata.UnmarshalBinary(data)
	if err != nil {
		return nil, err
	}

	// err = gob.NewDecoder(bytes.NewReader(data)).Decode(metadata)
	// if err != nil {
	// 	return nil, err
	// }
	return metadata, nil
}
