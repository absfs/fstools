package fstools

import (
	"errors"
	"os"
	slashpath "path"
	"path/filepath"
	"sort"

	"github.com/absfs/absfs"
)

type SortFunc func(i os.FileInfo, j os.FileInfo) bool

type Traversal int

const (
	BreadthTraversal Traversal = iota
	DepthTraversal
	PreOrderTraversal
	PostOrderTraversal
	KeyTraversal
)

type Options struct {
	Sort      bool
	Less      SortFunc
	Fast      bool
	Traversal Traversal
}

var defaultOptions = &Options{
	Sort:      false,
	Less:      nil,
	Fast:      false,
	Traversal: BreadthTraversal,
}

type nameSorter []os.FileInfo

func (s nameSorter) Len() int           { return len(s) }
func (s nameSorter) Swap(i, j int)      { s[i], s[j] = s[j], s[i] }
func (s nameSorter) Less(i, j int) bool { return s[i].Name() < s[j].Name() }

type funcSorter struct {
	infos []os.FileInfo
	fn    SortFunc
}

func (s funcSorter) Len() int           { return len(s.infos) }
func (s funcSorter) Swap(i, j int)      { s.infos[i], s.infos[j] = s.infos[j], s.infos[i] }
func (s funcSorter) Less(i, j int) bool { return s.fn(s.infos[i], s.infos[i]) }

func WalkWithOptions(fs absfs.Filer, options *Options, path string, fn filepath.WalkFunc) error {
	if options == nil {
		options = defaultOptions
	}

	info, err := fs.Stat(path)
	if err != nil {
		return nil
	}

	var infos []os.FileInfo
	if info.IsDir() {
		f, err := fs.OpenFile(path, os.O_RDONLY, 0700)
		if err != nil {
			return nil
		}
		infos, err = f.Readdir(-1)
		if err != nil {
			return err
		}
		f.Close()
	}

	err = fn(path, info, err)
	if err != nil {
		if err == filepath.SkipDir {
			return nil
		}
		return err
	}

	if options.Sort {
		if options.Less == nil {
			sort.Sort(nameSorter(infos))
		} else {
			sort.Sort(funcSorter{infos, options.Less})
		}
	}

	for _, nfo := range infos {
		if nfo.Name() == "." || nfo.Name() == ".." {
			continue
		}
		p := slashpath.Join(path, nfo.Name())
		err := WalkWithOptions(fs, options, p, fn)
		if err != nil {
			if err == filepath.SkipDir {
				continue
			}
			return err
		}
	}
	return nil
}

func Walk(filer absfs.Filer, path string, fn filepath.WalkFunc) error {
	return WalkWithOptions(filer, defaultOptions, path, fn)
}

type entry struct {
	depth  int
	parent string
	info   os.FileInfo
}

func (e *entry) Path() string {
	return slashpath.Join(e.parent, e.info.Name())
}

type entrystack []*entry

func (s *entrystack) push(e *entry) {
	(*s) = append((*s), e)
}

func (s *entrystack) pop() (e *entry) {
	if len((*s)) == 0 {
		return nil
	}
	e = (*s)[len((*s))-1]
	(*s) = (*s)[:len((*s))-1]
	return e
}

func (s *entrystack) peek() (e *entry) {
	if len((*s)) == 0 {
		return nil
	}
	return (*s)[len((*s))-1]
}

func (s *entrystack) empty() bool {
	return len((*s)) == 0
}

func (e *entry) listDirs(fs absfs.Filer, less SortFunc) ([]*entry, error) {
	if !e.info.IsDir() {
		return nil, errors.New("not a directory")
	}
	f, err := fs.OpenFile(e.Path(), os.O_RDONLY, 0700)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	list, err := f.Readdir(-1)
	if err != nil {
		return nil, err
	}

	if less != nil {
		sort.Sort(sort.Reverse(funcSorter{list, less}))
	}

	dirs := make([]*entry, len(list))
	for i := range list {
		dirs[i] = &entry{e.depth, e.Path(), list[i]}
	}

	return dirs, nil
}

func PreOrder(fs absfs.Filer, options *Options, path string, fn filepath.WalkFunc) error {
	info, err := fs.Stat(path)
	if err != nil {
		return nil
	}
	if info.Name() == "." || info.Name() == ".." {
		return nil
	}

	var stack entrystack

	stack.push(&entry{0, path, info})

	for !stack.empty() {
		e := stack.pop()
		err = fn(e.Path(), e.info, nil)
		if err != nil {
			if err != filepath.SkipDir {
				continue
			}
			return err
		}
		dirs, err := e.listDirs(fs, options.Less)
		if err != nil {
			return err
		}

		for _, entry := range dirs {
			stack.push(entry)
		}
	}

	return nil
}

func PostOrder(fs absfs.Filer, options *Options, path string, fn filepath.WalkFunc) error {

	info, err := fs.Stat(path)
	if err != nil {
		return nil
	}
	if info.Name() == "." || info.Name() == ".." {
		return nil
	}

	var stack entrystack

	node := &entry{0, path, info}

	for !(stack.empty() && node == nil) {

		//	2.1 Do following while `node` is not NULL
		if node != nil {

			//		a) Push nods's right children and then node to stack.
			dirs, err := node.listDirs(fs, options.Less)
			if err != nil {
				return err
			}

			if len(dirs) == 0 {
				stack.push(node)
				node = nil
				continue
			}
			for i := range dirs {
				stack.push(dirs[i])
			}
			left := stack.pop()
			stack.push(node)

			//		b) Set `node` as `node`'s left child.
			node = left
			// log.Infof("stack: %s", stack)
			continue
		}
		if stack.empty() {
			break
		}
		// log.Infof("node == nil stack: %s", stack)

		//	2.2 Pop an item from stack and set it as `node`.
		node = stack.pop()
		//		a) If the popped item has a right child and the right child
		//			is at top of stack, then remove the right child from stack,
		//			push the root back and set root as root's right child.

		right := stack.pop()
		if right != nil && right.depth == node.depth+1 {
			// log.Infof("right.Depth() %d == node.Depth() %d +1 %q, %q", right.Depth(), node.Depth(), right.Name, node.Name)
			stack.push(node)
			node = right
			continue
		}
		if right != nil {
			stack.push(right)
		}

		//		b) Else print root's data and set root as NULL.
		if node != nil {
			err := fn(node.Path(), node.info, nil)
			if err != nil {
				return err
			}
		}

		node = nil

	} //	2.3 Repeat steps 2.1 and 2.2 while stack is not empty.
	// push(&entry{0, id, name})

	// for !empty() {
	// 	e := peek()
	// 	dirs := tx.GetDirs(e.Id)
	// 	if dirs.Len() == 0 {
	// 		err = fn(e.Name, e.Id, nil)
	// 	}
	// 	sort.Sort(sort.Reverse(dirs))
	// 	for i := range dirs {
	// 		dirs[i].SetDepth(e.Depth() + 1)
	// 		push(&dirs[i])
	// 	}

	// 	err = fn(e.Name, e.Id, nil)
	// 	if err != nil {
	// 		if err != filepath.SkipDir {
	// 			continue
	// 		}
	// 		return err
	// 	}
	// 	dirs = tx.GetDirs(id)
	// 	if len(dirs) == 0 {
	// 		continue
	// 	}
	// 	sort.Sort(sort.Reverse(dirs))

	// 	for _, entry := range dirs {
	// 		entry.SetDepth(e.Depth() + 1)
	// 		push(&entry)
	// 	}
	// }
	return nil
}
