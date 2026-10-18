package update

import "golang.org/x/sys/unix"

func exchangeDirectories(from, to string) error {
	return unix.RenamexNp(from, to, unix.RENAME_SWAP)
}
