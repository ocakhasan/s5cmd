package command

import (
	errorpkg "github.com/peak/s5cmd/error"
	"github.com/peak/s5cmd/storage"
)

type Strategy interface {
	Compare(srcObject, dstObject *storage.Object) error
}

func NewStrategy(sizeOnly bool) Strategy {
	if sizeOnly {
		return &SizeOnly{}
	} else {
		return &SizeAndModification{}
	}
}

type SizeOnly struct{}

func (s *SizeOnly) Compare(srcObj, dstObj *storage.Object) error {
	if srcObj.Size == dstObj.Size {
		return errorpkg.ErrObjectSizesMatch
	}
	return nil
}

type SizeAndModification struct{}

func (sm *SizeAndModification) Compare(srcObj, dstObj *storage.Object) error {
	var stickyErr = errorpkg.ErrObjectSizesMatch
	// check size of objects
	if srcObj.Size != dstObj.Size {
		stickyErr = nil
	}

	srcMod, dstMod := srcObj.ModTime, dstObj.ModTime
	if !srcMod.After(*dstMod) {
		stickyErr = errorpkg.ErrObjectIsNewer
	} else {
		stickyErr = nil
	}

	return stickyErr
}
