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
	Traversal: PreOrderTraversal,
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
	switch options.Traversal {
	case BreadthTraversal:
		return BreadthOrder(fs, options, path, fn)
	case DepthTraversal:
		fallthrough
	case PreOrderTraversal:
		return PreOrder(fs, options, path, fn)
	case PostOrderTraversal:
		return PostOrder(fs, options, path, fn)
	case KeyTraversal:
		return KeyOrder(fs, options, path, fn)
	}
	return errors.New("unsupported traversal type")
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

func (s *entrystack) empty() bool {
	return len((*s)) == 0
}

type entryqueue []*entry

func (s *entryqueue) enqueue(e *entry) {
	(*s) = append((*s), e)
}

func (s *entryqueue) dequeue() (e *entry) {
	if len((*s)) == 0 {
		return nil
	}
	e = (*s)[0]
	(*s) = (*s)[1:]
	return e
}

func (s *entryqueue) peek() (e *entry) {
	return (*s)[0]
}

func (s *entryqueue) empty() bool {
	return len((*s)) == 0
}

var errNotDir error = errors.New("not a directory")

func (e *entry) listDirs(fs absfs.Filer, less SortFunc) ([]*entry, error) {
	if !e.info.IsDir() {
		return []*entry{}, errNotDir
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
		sort.Sort(funcSorter{list, less})
	} else {
		sort.Sort(sort.Reverse(nameSorter(list)))
	}

	var dirs []*entry
	for i := range list {
		if list[i].Name() == "." || list[i].Name() == ".." {
			continue
		}
		dirs = append(dirs, &entry{e.depth + 1, e.Path(), list[i]})
	}

	return dirs, nil
}

func KeyOrder(fs absfs.Filer, options *Options, path string, fn filepath.WalkFunc) error {
	return PreOrder(fs, options, path, func(path string, info os.FileInfo, err error) error {
		if info.IsDir() {
			return nil
		}
		return fn(path, info, err)
	})
}

func BreadthOrder(fs absfs.Filer, options *Options, path string, fn filepath.WalkFunc) error {
	if options == nil {
		options = defaultOptions
	}
	info, err := fs.Stat(path)
	if err != nil {
		return nil
	}

	var queue entryqueue

	queue.enqueue(&entry{0, filepath.Dir(path), info})
	for !queue.empty() {
		e := queue.dequeue()
		err = fn(e.Path(), e.info, nil)
		if err != nil {
			if err != filepath.SkipDir {
				continue
			}
			return err
		}
		dirs, err := e.listDirs(fs, options.Less)
		if err != nil && err != errNotDir {
			return err
		}

		for _, entry := range dirs {
			queue.enqueue(entry)
		}
	}
	return nil
}

func PreOrder(fs absfs.Filer, options *Options, path string, fn filepath.WalkFunc) error {

	if options == nil {
		options = defaultOptions
	}
	info, err := fs.Stat(path)
	if err != nil {
		return err
	}
	var stack entrystack

	stack.push(&entry{0, filepath.Dir(path), info})
	for !stack.empty() {
		e := stack.pop()
		err = fn(e.Path(), e.info, nil)
		if err != nil {
			if err == filepath.SkipDir {
				continue
			}
			return err
		}

		dirs, err := e.listDirs(fs, options.Less)
		if err != nil && err != errNotDir {
			return err
		}

		for _, entry := range dirs {
			stack.push(entry)
		}
	}

	return nil
}

func PostOrder(fs absfs.Filer, options *Options, path string, fn filepath.WalkFunc) error {
	if options == nil {
		options = defaultOptions
	}
	info, err := fs.Stat(path)
	if err != nil {
		return nil
	}

	//	1.1 Create an empty stack
	var stack entrystack
	node := &entry{0, filepath.Dir(path), info}

	for !(stack.empty() && node == nil) {
		//	2.1 Do following while `node` is not NULL
		if node != nil {
			//		a) Push nods's right children and then node to stack.
			dirs, err := node.listDirs(fs, options.Less)
			if err != nil && err != errNotDir {
				return err
			}

			if len(dirs) == 0 {
				stack.push(node)
				node = nil
				continue
			}

			for _, dir := range dirs {
				stack.push(dir)
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

	}
	return nil
}
