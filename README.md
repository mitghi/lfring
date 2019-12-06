[![GoDoc](https://godoc.org/github.com/mitghi/lfring?status.svg)](https://godoc.org/github.com/mitghi/lfring)

# lfring

Lock-Free MRMW Ring Buffer.  It uses an implementation of Multi-Word Compare-and-Swap atomic operation, removes references by setting the associated slot to `nil` without losing consistency; ensures ordering and garbage collection.
