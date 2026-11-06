// Copyright Dokimasia B.V. 2026
// SPDX-License-Identifier: MIT

package medium

import (
	"io"
	"io/fs"
	"maps"
	"os"
	"path"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"
)

// root is the name of the root directory of a Memory.
const root = "."

// Memory is an FS in memory that keeps the data that a sync made durable apart
// from the data written since, as the package states the model of
// persistence. Its methods crash it, kill its process, tear a write, corrupt a
// byte at rest, fill it, fail a call and block its calls, so a check exercises
// what a crash, the kill of a process, a power loss in the middle of a write,
// a flipped bit, a full disk, a failed read or write and a slow disk leave a
// durable subject. A Memory is safe for concurrent use.
type Memory struct {
	// current maps the name of each entry that the process reads to its
	// node, and durable maps the name of each entry that a SyncDir of its
	// directory made durable. Both contain the root.
	current, durable map[string]*node

	// epoch is the epoch of the process that the medium serves, which each
	// crash replaces. A file that the medium opened in an earlier epoch is
	// closed.
	epoch *int

	// fault is the failure that FailBefore, FailAfter or FailPart armed, and
	// nil without one.
	fault *failure

	// hold is the hold of the calls that Hold started, and nil without one.
	hold *hold

	mu sync.Mutex

	// points counts the persistence points, and crashAt is the persistence
	// point in place of whose next operation the medium crashes, or -1.
	points, crashAt int

	// tear is the count of bytes that the next write keeps at a crash, or
	// -1.
	tear int

	// written counts the bytes of the writes, and limit is the count at
	// which the writes stop, or -1.
	written, limit int64

	// down reports that the medium crashed in place of a persistence point,
	// so it serves no call until Crash.
	down bool
}

// The compiler checks that a Memory is an FS.
var _ FS = (*Memory)(nil)

// NewMemory returns an empty Memory.
func NewMemory() *Memory {
	top := &node{dir: true, mode: fs.ModePerm}
	return &Memory{
		current: map[string]*node{root: top}, durable: map[string]*node{root: top},
		epoch: new(int), crashAt: -1, tear: -1, limit: -1,
	}
}

// Crash drops every write that a sync did not cover, and every entry of a
// directory that a SyncDir did not cover. A torn write keeps the pending
// writes of its file before it and its own first bytes. Each open file is
// closed, and its later calls fail with ErrCrashed. The medium then serves the
// calls of a new process. A crash after CrashAt does not drop anything more.
// Crash ends a crash that CrashAt armed and that did not happen, the failure
// that FailBefore, FailAfter or FailPart armed, and a hold. Each call that the
// hold blocked fails with ErrCrashed.
func (m *Memory) Crash() {
	m.crash(nil)
}

// CrashKeeping crashes the medium as Crash does, and keeps of each file name
// the first keep(name, pending) of its pending writes and truncates, in the
// order of the calls. It drops the other pending calls of the file. A count of
// 0 drops every pending call, and a count of pending or more keeps every one.
// The last kept call of a file is torn where Tear armed it, and an armed write
// that a later kept call follows is kept whole. The pending entries of each
// directory are kept or dropped as a whole, by keep of the name of the
// directory with a count of 1.
//
// CrashKeeping calls keep for the directories and then for the files that the
// crash keeps, each in the order of their names, and only for those whose
// entries or calls are pending. keep must not call the medium. A medium that
// crashed in place of a persistence point dropped its pending state already,
// so CrashKeeping calls keep for no name then.
func (m *Memory) CrashKeeping(keep func(name string, pending int) int) {
	m.crash(keep)
}

// Kill ends the process that the medium serves, as a kill of the process does.
// Each open file is closed, and its later calls fail with ErrCrashed. The data
// of every write remains, durable or pending, so a later crash drops what no
// sync covered. Kill ends what Crash ends: a crash that CrashAt armed, the
// failure that FailBefore, FailAfter or FailPart armed, and a hold. Each call
// that the hold blocked fails with ErrCrashed. Kill also ends a tear that no
// write took, and a medium that crashed in place of a persistence point serves
// the calls of the next process.
func (m *Memory) Kill() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.epoch, m.tear = new(int), -1
	m.restart()
}

// CrashAt makes the medium crash in place of its persistence point point+1:
// that Sync or SyncDir does not persist anything and fails with ErrCrashed,
// and every later call of the medium and of its files fails with ErrCrashed
// until Crash. Points counts the persistence points.
func (m *Memory) CrashAt(point int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.crashAt = point
}

// Tear makes the next write of a file keep only its first n bytes when a crash
// follows it before a sync of its file.
func (m *Memory) Tear(n int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.tear = n
}

// Corrupt flips the lowest bit of the durable byte of the file name at the
// offset, so a later read returns the flipped byte where no pending write
// covers it. An offset outside the durable data of the file, and a name of no
// file, corrupt nothing.
func (m *Memory) Corrupt(name string, offset int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	// A directory has no content, so its durable data is empty.
	n, found := m.current[name]
	if !found || offset < 0 || offset >= int64(len(n.durable)) {
		return
	}
	n.durable[offset] ^= 1
	n.current = n.replay()
}

// Fill makes every write after the next n bytes fail with an error that wraps
// syscall.ENOSPC. A write that crosses the limit writes the bytes that fit and
// returns their count beside the error. A negative n lifts the limit.
func (m *Memory) Fill(n int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.limit = -1
	if n >= 0 {
		m.limit = m.written + n
	}
}

// FailBefore makes the next call of the medium or of one of its files fail
// with an error that wraps err, and the call does not change the medium. A
// later FailBefore, FailAfter or FailPart replaces it.
func (m *Memory) FailBefore(err error) {
	m.arm(&failure{err: err, stage: before})
}

// FailAfter makes the next call of the medium or of one of its files take its
// whole effect, and then fail with an error that wraps err. The call returns
// its results beside the error. A call that fails on its own returns its own
// error. A later FailBefore, FailAfter or FailPart replaces it.
func (m *Memory) FailAfter(err error) {
	m.arm(&failure{err: err, stage: after})
}

// FailPart makes the next write of a file of the medium write its first n
// bytes, and then fail with an error that wraps err. The write returns the
// count of the bytes that it wrote beside the error, and writes no byte for an
// n below 0. A later FailBefore, FailAfter or FailPart replaces it.
func (m *Memory) FailPart(n int, err error) {
	m.arm(&failure{err: err, stage: part, part: n})
}

// Hold blocks each later call of the medium and of its files until Release or
// Crash. A call that a crash ends fails with ErrCrashed. A blocked call waits
// on a channel that Hold makes, so a call that blocks in a testing/synctest
// bubble whose goroutine called Hold is durably blocked. A Hold while a hold
// is in force changes nothing.
func (m *Memory) Hold() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.hold == nil {
		m.hold = &hold{released: make(chan struct{})}
	}
}

// Release ends the hold of the medium, and each blocked call goes on.
// Release without a hold changes nothing.
func (m *Memory) Release() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.release(false)
}

// Held reports whether a hold of Hold is in force. A Release and a crash end
// the hold.
func (m *Memory) Held() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.hold != nil
}

// Points returns the number of persistence points so far: each Sync and each
// SyncDir that returned nil.
func (m *Memory) Points() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.points
}

// Written returns the number of bytes that the writes of the medium wrote so
// far.
func (m *Memory) Written() int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.written
}

// OpenFile opens the file name with the flags of os.OpenFile, and creates it
// with perm where flag contains os.O_CREATE. A created file is a pending
// entry of its directory.
func (m *Memory) OpenFile(name string, flag int, perm fs.FileMode) (File, error) {
	var opened File
	err := m.serve("open", name, func() error {
		if err := m.check("open", name); err != nil {
			return err
		}
		n, exists := m.current[name]
		switch {
		case exists && flag&os.O_CREATE != 0 && flag&os.O_EXCL != 0:
			return pathError("open", name, fs.ErrExist)
		case exists && n.dir:
			return pathError("open", name, syscall.EISDIR)
		case !exists && flag&os.O_CREATE == 0:
			return pathError("open", name, fs.ErrNotExist)
		case !exists:
			if err := m.parent("open", name); err != nil {
				return err
			}
			n = &node{mode: perm.Perm()}
			m.current[name] = n
		}
		if flag&os.O_TRUNC != 0 {
			n.apply(op{off: 0, truncate: true, keep: -1})
		}
		opened = &file{m: m, n: n, name: name, flag: flag, epoch: m.epoch}
		return nil
	})
	return opened, err
}

// Remove removes the file or the empty directory name from the pending
// entries of its directory.
func (m *Memory) Remove(name string) error {
	return m.serve("remove", name, func() error {
		if err := m.check("remove", name); err != nil {
			return err
		}
		_, exists := m.current[name]
		switch {
		case name == root:
			return pathError("remove", name, fs.ErrInvalid)
		case !exists:
			return pathError("remove", name, fs.ErrNotExist)
		case m.children(name):
			return pathError("remove", name, syscall.ENOTEMPTY)
		}
		delete(m.current, name)
		return nil
	})
}

// Rename moves the file or the directory oldName to newName in the pending
// entries of their directories, and replaces a file at newName, or an empty
// directory at newName with a directory.
func (m *Memory) Rename(oldName, newName string) error {
	return m.serve("rename", oldName, func() error {
		if err := m.renamable(oldName, newName); err != nil {
			return &os.LinkError{Op: "rename", Old: oldName, New: newName, Err: err}
		}
		moved := map[string]*node{}
		for name, n := range m.current {
			if name == oldName || strings.HasPrefix(name, oldName+"/") {
				moved[newName+strings.TrimPrefix(name, oldName)] = n
				delete(m.current, name)
			}
		}
		maps.Copy(m.current, moved)
		return nil
	})
}

// MkdirAll creates the directory name and each parent that it lacks, each a
// pending entry of its directory.
func (m *Memory) MkdirAll(name string, perm fs.FileMode) error {
	return m.serve("mkdir", name, func() error {
		if err := m.check("mkdir", name); err != nil {
			return err
		}
		parts := strings.Split(name, "/")
		for i := range parts {
			dir := strings.Join(parts[:i+1], "/")
			n, exists := m.current[dir]
			switch {
			case !exists:
				m.current[dir] = &node{dir: true, mode: perm.Perm()}
			case !n.dir:
				return pathError("mkdir", dir, syscall.ENOTDIR)
			}
		}
		return nil
	})
}

// ReadDir returns the entries of the directory name, sorted by name.
func (m *Memory) ReadDir(name string) ([]fs.DirEntry, error) {
	var out []fs.DirEntry
	err := m.serve("readdir", name, func() error {
		if err := m.dir("readdir", name); err != nil {
			return err
		}
		for entry, n := range m.current {
			if entry != root && path.Dir(entry) == name {
				out = append(out, fs.FileInfoToDirEntry(n.info(entry)))
			}
		}
		slices.SortFunc(out, func(a, b fs.DirEntry) int { return strings.Compare(a.Name(), b.Name()) })
		return nil
	})
	return out, err
}

// Stat returns the information of the file or the directory name.
func (m *Memory) Stat(name string) (fs.FileInfo, error) {
	var stat fs.FileInfo
	err := m.serve("stat", name, func() error {
		if err := m.check("stat", name); err != nil {
			return err
		}
		n, exists := m.current[name]
		if !exists {
			return pathError("stat", name, fs.ErrNotExist)
		}
		stat = n.info(name)
		return nil
	})
	return stat, err
}

// SyncDir makes the pending entries of the directory name durable. It is a
// persistence point.
func (m *Memory) SyncDir(name string) error {
	return m.serve("syncdir", name, func() error {
		if err := m.dir("syncdir", name); err != nil {
			return err
		}
		if err := m.persist("syncdir", name); err != nil {
			return err
		}
		m.syncEntries(name)
		return nil
	})
}

// syncEntries makes the pending entries of the directory name durable.
func (m *Memory) syncEntries(name string) {
	// The root is in the root, so a sync of the root keeps the root.
	in := func(entry string) bool { return path.Dir(entry) == name }
	maps.DeleteFunc(m.durable, func(entry string, _ *node) bool { return in(entry) })
	for entry, n := range m.current {
		if in(entry) {
			m.durable[entry] = n
		}
	}
}

// entriesOf returns the entries of the directory name among entries.
func entriesOf(entries map[string]*node, name string) map[string]*node {
	out := map[string]*node{}
	for entry, n := range entries {
		if path.Dir(entry) == name {
			out[entry] = n
		}
	}
	return out
}

// crash crashes the medium, and keeps of each file and each directory what
// keep chooses, or what Crash keeps for a nil keep.
func (m *Memory) crash(keep func(name string, pending int) int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.down {
		m.drop(keep)
	}
	m.restart()
}

// restart makes the medium serve the calls of a new process. It ends a crash
// that CrashAt armed, the failure that FailBefore, FailAfter or FailPart armed,
// and a hold, whose blocked calls fail with ErrCrashed.
func (m *Memory) restart() {
	m.down, m.crashAt, m.fault = false, -1, nil
	m.release(true)
}

// keepEntries makes durable the pending entries of each directory for which
// keep of its name and 1 returns more than 0. It asks for each durable
// directory that a crash keeps and whose entries differ from its durable
// entries, in the order of the names. A name sorts after the names of its
// parents, so a directory whose entry a parent keeps is asked after that
// parent.
func (m *Memory) keepEntries(keep func(name string, pending int) int) {
	names := maps.Clone(m.current)
	maps.Copy(names, m.durable)
	for _, name := range slices.Sorted(maps.Keys(names)) {
		d, durable := m.durable[name]
		if !durable || !d.dir || !m.rooted(name) {
			continue
		}
		if !maps.Equal(entriesOf(m.current, name), entriesOf(m.durable, name)) && keep(name, 1) > 0 {
			m.syncEntries(name)
		}
	}
}

// serve runs body, a call of the operation op on name, with the medium
// locked. The call waits while a hold is in force, and fails with ErrCrashed
// where a crash ends the hold. It fails without its effect where FailBefore
// armed a failure, and after its effect where FailAfter armed one.
func (m *Memory) serve(op, name string, body func() error) error {
	if err := m.enter(op, name); err != nil {
		return err
	}
	defer m.mu.Unlock()
	err := body()
	if f := m.fault; f != nil && f.stage == after {
		m.fault = nil
		if err == nil {
			err = pathError(op, name, f.err)
		}
	}
	return err
}

// enter locks the medium for a call of the operation op on name. It waits
// while a hold is in force, and returns ErrCrashed for a call that a crash
// ended during the hold. It returns the error of a failure that FailBefore
// armed, and ends the failure. It returns with the medium locked where it
// returns nil.
func (m *Memory) enter(op, name string) error {
	m.mu.Lock()
	for m.hold != nil {
		h := m.hold
		m.mu.Unlock()
		<-h.released
		m.mu.Lock()
		if h.crashed {
			m.mu.Unlock()
			return pathError(op, name, ErrCrashed)
		}
	}
	if f := m.fault; f != nil && f.stage == before {
		m.fault = nil
		m.mu.Unlock()
		return pathError(op, name, f.err)
	}
	return nil
}

// arm arms the failure f in place of an earlier one.
func (m *Memory) arm(f *failure) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.fault = f
}

// release ends the hold of the medium, for a crash where crashed is true.
func (m *Memory) release(crashed bool) {
	if m.hold != nil {
		m.hold.crashed = crashed
		close(m.hold.released)
		m.hold = nil
	}
}

// renamable returns the error of a rename of oldName to newName that the
// medium refuses.
func (m *Memory) renamable(oldName, newName string) error {
	for _, name := range []string{oldName, newName} {
		if err := m.check("rename", name); err != nil {
			return err
		}
	}
	n, exists := m.current[oldName]
	target, replaces := m.current[newName]
	switch {
	case oldName == root || newName == root:
		return fs.ErrInvalid
	case !exists:
		return fs.ErrNotExist
	case strings.HasPrefix(newName, oldName+"/"):
		return syscall.EINVAL
	case !replaces:
		return m.parent("rename", newName)
	case n.dir && !target.dir:
		return syscall.ENOTDIR
	case !n.dir && target.dir:
		return syscall.EISDIR
	case m.children(newName):
		return syscall.ENOTEMPTY
	}
	return nil
}

// check returns the error of a call of the operation op on name: ErrCrashed
// for a medium that crashed in place of a persistence point, and fs.ErrInvalid
// for a name that fs.ValidPath refuses.
func (m *Memory) check(op, name string) error {
	switch {
	case m.down:
		return pathError(op, name, ErrCrashed)
	case !fs.ValidPath(name):
		return pathError(op, name, fs.ErrInvalid)
	}
	return nil
}

// dir returns the error of a call of the operation op on name for a name of
// no directory, as check does for any name.
func (m *Memory) dir(op, name string) error {
	if err := m.check(op, name); err != nil {
		return err
	}
	n, exists := m.current[name]
	switch {
	case !exists:
		return pathError(op, name, fs.ErrNotExist)
	case !n.dir:
		return pathError(op, name, syscall.ENOTDIR)
	}
	return nil
}

// parent returns the error of the operation op on name whose directory does
// not exist or is a file.
func (m *Memory) parent(op, name string) error {
	return m.dir(op, path.Dir(name))
}

// children reports whether the directory name has an entry.
func (m *Memory) children(name string) bool {
	for entry := range m.current {
		if strings.HasPrefix(entry, name+"/") {
			return true
		}
	}
	return false
}

// persist counts a persistence point of the operation op on name. It crashes
// the medium in place of the operation after the point that CrashAt armed,
// and returns ErrCrashed then.
func (m *Memory) persist(op, name string) error {
	if m.points == m.crashAt {
		m.drop(nil)
		m.down, m.crashAt = true, -1
		return pathError(op, name, ErrCrashed)
	}
	m.points++
	return nil
}

// drop drops the pending state of each file and the pending entries of each
// directory, as a crash does. An entry survives where each of its parents is
// a durable directory. With keep, each directory keeps its pending entries and
// each file its first pending calls as keep chooses. Without keep, a file
// keeps its pending calls up to a torn write. The files of the epoch are
// closed.
func (m *Memory) drop(keep func(name string, pending int) int) {
	if keep != nil {
		m.keepEntries(keep)
	}
	survivors := map[string]*node{}
	for name, n := range m.durable {
		if m.rooted(name) {
			survivors[name] = n
		}
	}
	for _, name := range slices.Sorted(maps.Keys(survivors)) {
		n := survivors[name]
		count := n.torn() + 1
		if keep != nil && len(n.pending) > 0 {
			count = min(max(keep(name, len(n.pending)), 0), len(n.pending))
		}
		n.recover(count)
	}
	m.current, m.durable = survivors, maps.Clone(survivors)
	m.tear = -1
	m.epoch = new(int)
}

// rooted reports whether each parent of the entry name is a durable
// directory.
func (m *Memory) rooted(name string) bool {
	for name != root {
		name = path.Dir(name)
		if d, durable := m.durable[name]; !durable || !d.dir {
			return false
		}
	}
	return true
}

// write writes data at the offset off of the file of n, opened as name, and
// returns the count of bytes that it wrote. It writes the first bytes that
// FailPart armed, and returns an error that wraps its error beside them. It
// writes the bytes that fit under the limit of Fill, and returns an error that
// wraps syscall.ENOSPC beside them. The write is torn where Tear armed it.
func (m *Memory) write(n *node, name string, data []byte, off int64) (int, error) {
	count := int64(len(data))
	var err error
	if f := m.fault; f != nil && f.stage == part {
		m.fault = nil
		count = min(count, max(0, int64(f.part)))
		err = pathError("write", name, f.err)
	}
	if m.limit >= 0 && m.written+count > m.limit {
		count = max(0, m.limit-m.written)
		err = pathError("write", name, syscall.ENOSPC)
	}
	if count == 0 {
		return 0, err
	}
	o := op{data: slices.Clone(data[:count]), off: off, keep: m.tear}
	m.tear = -1
	n.apply(o)
	m.written += count
	return int(count), err
}

// stage is when an armed failure fails its call.
type stage int

// The stages of an armed failure.
const (
	// before fails the next call without its effect.
	before stage = iota
	// after fails the next call after its whole effect.
	after
	// part fails the next write after its first bytes.
	part
)

// failure is a failure that FailBefore, FailAfter or FailPart armed.
type failure struct {
	// err is the error that the failed call wraps.
	err error
	// stage is when the call fails.
	stage stage
	// part is the count of bytes that a write writes before it fails, for the
	// stage part.
	part int
}

// hold blocks the calls of a Memory until its release.
type hold struct {
	// released is closed when the hold ends.
	released chan struct{}
	// crashed reports that a crash ended the hold.
	crashed bool
}

// node is a file or a directory of a Memory.
type node struct {
	// durable is the content that the last sync of the file made durable,
	// current is the content that the process reads, and pending are the
	// operations since the last sync, in order.
	durable, current []byte
	pending          []op
	mode             fs.FileMode
	dir              bool
}

// apply applies the pending operation o to the file of n.
func (n *node) apply(o op) {
	n.pending = append(n.pending, o)
	n.current = o.applied(n.current)
}

// replay returns the durable content of the file of n with each pending
// operation applied.
func (n *node) replay() []byte {
	content := slices.Clone(n.durable)
	for _, o := range n.pending {
		content = o.applied(content)
	}
	return content
}

// sync makes the content of the file of n durable.
func (n *node) sync() {
	n.durable, n.pending = slices.Clone(n.current), nil
}

// torn returns the index of the first pending write that Tear armed, and -1
// without one.
func (n *node) torn() int {
	return slices.IndexFunc(n.pending, func(o op) bool { return o.keep >= 0 })
}

// recover makes the content of the file of n what a crash keeps: its durable
// content with its first count pending operations applied. The last of them
// keeps its first bytes where Tear armed it.
func (n *node) recover(count int) {
	if count > 0 {
		kept := n.pending[:count]
		if last := &kept[count-1]; last.keep >= 0 {
			last.data = last.data[:min(last.keep, len(last.data))]
		}
		n.pending = kept
		n.durable = n.replay()
	}
	n.current, n.pending = slices.Clone(n.durable), nil
}

// info returns the information of the entry name of n.
func (n *node) info(name string) fs.FileInfo {
	mode := n.mode
	if n.dir {
		mode |= fs.ModeDir
	}
	return info{name: path.Base(name), size: int64(len(n.current)), mode: mode}
}

// op is a pending write or truncate of a file. A write writes data at off,
// and a truncate sets the size of the file to off. keep is the count of bytes
// that a crash keeps of a torn write, and -1 for any other operation.
type op struct {
	data     []byte
	off      int64
	keep     int
	truncate bool
}

// applied returns content with o applied. A write beyond the end of the file
// fills the gap with zeros.
func (o *op) applied(content []byte) []byte {
	if o.truncate {
		return resized(content, o.off)
	}
	content = resized(content, max(o.off+int64(len(o.data)), int64(len(content))))
	copy(content[o.off:], o.data)
	return content
}

// resized returns content with the size size, cut or filled with zeros.
func resized(content []byte, size int64) []byte {
	size0 := int64(len(content))
	return append(content[:min(size, size0)], make([]byte, max(0, size-size0))...)
}

// file is an open file of a Memory.
type file struct {
	m     *Memory
	n     *node
	epoch *int
	name  string
	off   int64
	flag  int

	closed bool
}

// Read reads from the offset of the file and moves the offset. It returns
// io.EOF only for a read that reads nothing, as a file of the os package does.
func (f *file) Read(p []byte) (int, error) {
	var n int
	err := f.m.serve("read", f.name, func() error {
		if err := f.readable("read"); err != nil {
			return err
		}
		got, err := f.at(p, f.off)
		n = got
		f.off += int64(got)
		if got > 0 {
			return nil
		}
		return err
	})
	return n, err
}

// ReadAt reads from the offset off of the file.
func (f *file) ReadAt(p []byte, off int64) (int, error) {
	var n int
	err := f.m.serve("read", f.name, func() error {
		if err := f.readable("read"); err != nil {
			return err
		}
		if off < 0 {
			return pathError("read", f.name, syscall.EINVAL)
		}
		got, err := f.at(p, off)
		n = got
		return err
	})
	return n, err
}

// Write writes at the offset of the file, or at its end for a file opened
// with os.O_APPEND, and moves the offset.
func (f *file) Write(p []byte) (int, error) {
	var n int
	err := f.m.serve("write", f.name, func() error {
		if err := f.writable("write"); err != nil {
			return err
		}
		if f.flag&os.O_APPEND != 0 {
			f.off = int64(len(f.n.current))
		}
		wrote, err := f.m.write(f.n, f.name, p, f.off)
		n = wrote
		f.off += int64(wrote)
		return err
	})
	return n, err
}

// WriteAt writes at the offset off of the file. A file opened with
// os.O_APPEND refuses it, as the os package does.
func (f *file) WriteAt(p []byte, off int64) (int, error) {
	var n int
	err := f.m.serve("write", f.name, func() error {
		if err := f.writable("write"); err != nil {
			return err
		}
		if f.flag&os.O_APPEND != 0 || off < 0 {
			return pathError("write", f.name, syscall.EINVAL)
		}
		wrote, err := f.m.write(f.n, f.name, p, off)
		n = wrote
		return err
	})
	return n, err
}

// Seek sets the offset of the file.
func (f *file) Seek(offset int64, whence int) (int64, error) {
	var at int64
	err := f.m.serve("seek", f.name, func() error {
		if err := f.check("seek"); err != nil {
			return err
		}
		base := map[int]int64{io.SeekStart: 0, io.SeekCurrent: f.off, io.SeekEnd: int64(len(f.n.current))}
		from, valid := base[whence]
		if !valid || from+offset < 0 {
			return pathError("seek", f.name, syscall.EINVAL)
		}
		f.off = from + offset
		at = f.off
		return nil
	})
	return at, err
}

// Sync makes the content of the file durable. It is a persistence point.
func (f *file) Sync() error {
	return f.m.serve("sync", f.name, func() error {
		if err := f.check("sync"); err != nil {
			return err
		}
		if err := f.m.persist("sync", f.name); err != nil {
			return err
		}
		f.n.sync()
		return nil
	})
}

// Truncate sets the size of the file to size.
func (f *file) Truncate(size int64) error {
	return f.m.serve("truncate", f.name, func() error {
		if err := f.writable("truncate"); err != nil {
			return err
		}
		if size < 0 {
			return pathError("truncate", f.name, syscall.EINVAL)
		}
		f.n.apply(op{off: size, truncate: true, keep: -1})
		return nil
	})
}

// Stat returns the information of the file.
func (f *file) Stat() (fs.FileInfo, error) {
	var stat fs.FileInfo
	err := f.m.serve("stat", f.name, func() error {
		if err := f.check("stat"); err != nil {
			return err
		}
		stat = f.n.info(f.name)
		return nil
	})
	return stat, err
}

// Close closes the file.
func (f *file) Close() error {
	return f.m.serve("close", f.name, func() error {
		if err := f.check("close"); err != nil {
			return err
		}
		f.closed = true
		return nil
	})
}

// at copies the content of the file at the offset off into p, and returns
// io.EOF where the file ends before p is full.
func (f *file) at(p []byte, off int64) (int, error) {
	content := f.n.current
	n := copy(p, content[min(off, int64(len(content))):])
	if n < len(p) {
		return n, io.EOF
	}
	return n, nil
}

// check returns the error of a call of the operation op on the file:
// ErrCrashed for a file of an earlier epoch, and fs.ErrClosed for a closed
// file. A crash in place of a persistence point replaces the epoch, so a file
// of a crashed medium is of an earlier epoch.
func (f *file) check(op string) error {
	switch {
	case f.epoch != f.m.epoch:
		return pathError(op, f.name, ErrCrashed)
	case f.closed:
		return pathError(op, f.name, fs.ErrClosed)
	}
	return nil
}

// readable returns the error of a read of a file that was not opened for
// reading, as check does for any file.
func (f *file) readable(op string) error {
	if err := f.check(op); err != nil {
		return err
	}
	if f.flag&os.O_WRONLY != 0 {
		return pathError(op, f.name, syscall.EBADF)
	}
	return nil
}

// writable returns the error of a write of a file that was not opened for
// writing, as check does for any file.
func (f *file) writable(op string) error {
	if err := f.check(op); err != nil {
		return err
	}
	if f.flag&(os.O_WRONLY|os.O_RDWR) == 0 {
		return pathError(op, f.name, syscall.EBADF)
	}
	return nil
}

// info is the information of an entry of a Memory. Its modification time is
// the zero time, because the medium has no clock.
type info struct {
	name string
	size int64
	mode fs.FileMode
}

// Name returns the base name of the entry.
func (i info) Name() string { return i.name }

// Size returns the size of a file in bytes.
func (i info) Size() int64 { return i.size }

// Mode returns the mode of the entry.
func (i info) Mode() fs.FileMode { return i.mode }

// ModTime returns the zero time.
func (info) ModTime() time.Time { return time.Time{} }

// IsDir reports whether the entry is a directory.
func (i info) IsDir() bool { return i.mode.IsDir() }

// Sys returns nil.
func (info) Sys() any { return nil }

// pathError returns the error of the operation op on name that wraps err.
func pathError(op, name string, err error) error {
	return &fs.PathError{Op: op, Path: name, Err: err}
}
