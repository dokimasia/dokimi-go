// Copyright Dokimasia B.V. 2026
// SPDX-License-Identifier: MIT

// Package medium is the storage of a durable subject: a filesystem with an
// explicit sync, implemented over the operating system and in memory.
//
// A subject writes through an [FS], and its production code passes [OS]. A
// check passes a [Memory], which keeps the data that a sync made durable apart
// from the data written since. A crash of a Memory drops every write that a
// sync did not cover, so a check reads back what a power loss keeps. A kill of
// its process keeps every write, as the page cache of the operating system
// does. A Memory can also crash at a persistence point, keep a part of the
// pending writes at a crash, tear a write, flip a bit of a file at rest and
// run out of space. It can fail a call before, after or in the middle of its
// effect, and block each call until a release.
//
// # The model of persistence
//
// A Memory models the guarantees that POSIX states for fsync:
//
//   - A write, a truncate and a create change the pending state of a file.
//     File.Sync makes the pending state of the file durable.
//   - A create, a remove and a rename change the pending entries of their
//     directories. FS.SyncDir makes the pending entries of a directory
//     durable. A rename is durable once both of its directories are synced.
//   - A crash drops the pending state of each file and the pending entries of
//     each directory. A torn write keeps the pending writes of its file before
//     it and its own first bytes.
//   - A crash by [Memory.CrashKeeping] keeps the first pending calls of each
//     file in the order of the calls, as a disk that wrote them before the
//     power loss keeps them. It keeps or drops the pending entries of each
//     directory as a whole.
//
// A name is a slash-separated path relative to the root of an FS, as
// [fs.ValidPath] states it. The module imports the standard library alone.
package medium
