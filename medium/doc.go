// Copyright Dokimasia B.V. 2026
// SPDX-License-Identifier: MIT

// Package medium is the storage of a durable subject: a filesystem with an
// explicit sync, implemented over the operating system and in memory.
//
// A subject writes through an [FS], and its production code passes [OS]. A
// check passes a [Memory], which keeps the data that a sync made durable apart
// from the data written since. A crash of a Memory drops every write that a
// sync did not cover, so a check reads back what a crash of the process keeps.
// A Memory can also crash at a persistence point, tear a write, flip a bit of
// a file at rest and run out of space.
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
//
// A name is a slash-separated path relative to the root of an FS, as
// [fs.ValidPath] states it. The module imports the standard library alone.
package medium
