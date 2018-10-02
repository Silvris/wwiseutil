// Package bnk implements access to the Wwise SoundBank file format.
package bnk

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"strings"
)

// A File represents an open Wwise SoundBank.
type File struct {
	closer            io.Closer
	BankHeaderSection *BankHeaderSection
	IndexSection      *DataIndexSection
	DataSection       *DataSection
	Others            []*UnknownSection
}

// A ReplacementWem defines a wem to be replaced into an original SoundBank File.
type ReplacementWem struct {
	// The reader pointing to the contents of the new wem.
	Wem io.ReaderAt
	// The index, where zero is the first wem, into the original SoundBank's wems
	// to replace.
	WemIndex int
	// The number of bytes to read in for this wem.
	Length int64
}

// NewFile creates a new File for access Wwise SoundBank files. The file is
// expected to start at position 0 in the io.ReaderAt.
func NewFile(r io.ReaderAt) (*File, error) {
	bnk := new(File)

	sr := io.NewSectionReader(r, 0, math.MaxInt64)
	for {
		hdr := new(SectionHeader)
		err := binary.Read(sr, binary.LittleEndian, hdr)
		if err != nil {
			if err == io.EOF {
				break
			}
			return nil, err
		}

		switch id := hdr.Identifier; id {
		case bkhdHeaderId:
			sec, err := hdr.NewBankHeaderSection(sr)
			if err != nil {
				return nil, err
			}
			bnk.BankHeaderSection = sec
		case didxHeaderId:
			sec, err := hdr.NewDataIndexSection(sr)
			if err != nil {
				return nil, err
			}
			bnk.IndexSection = sec
		case dataHeaderId:
			sec, err := hdr.NewDataSection(sr, bnk.IndexSection)
			if err != nil {
				return nil, err
			}
			bnk.DataSection = sec
		default:
			sec, err := hdr.NewUnknownSection(sr)
			if err != nil {
				return nil, err
			}
			bnk.Others = append(bnk.Others, sec)
		}
	}

	if bnk.DataSection == nil {
		return nil, errors.New("There are no wems stored within this SoundBank.")
	}

	return bnk, nil
}

// WriteTo writes the full contents of this File to the Writer specified by w.
func (bnk *File) WriteTo(w io.Writer) (written int64, err error) {
	written, err = bnk.BankHeaderSection.WriteTo(w)
	if err != nil {
		return
	}
	n, err := bnk.IndexSection.WriteTo(w)
	if err != nil {
		return
	}
	written += n
	n, err = bnk.DataSection.WriteTo(w)
	if err != nil {
		return
	}
	written += n
	for _, other := range bnk.Others {
		n, err = other.WriteTo(w)
		if err != nil {
			return
		}
		written += n
	}
	return written, err
}

// Open opens the File at the specified path using os.Open and prepares it for
// use as a Wwise SoundBank file.
func Open(path string) (*File, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	bnk, err := NewFile(f)
	if err != nil {
		f.Close()
		return nil, err
	}
	bnk.closer = f
	return bnk, nil
}

// Close closes the File
// If the File was created using NewFile directly instead of Open,
// Close has no effect.
func (bnk *File) Close() error {
	var err error
	if bnk.closer != nil {
		err = bnk.closer.Close()
		bnk.closer = nil
	}
	return err
}

// ReplaceWem replaces the wem of File at index i, reading the wem, with
// specified length in from r.
func (bnk *File) ReplaceWems(replacements ...*ReplacementWem) {
	for _, r := range replacements {
		length := r.Length
		wem := bnk.DataSection.Wems[r.WemIndex]
		oldLength := int64(wem.Descriptor.Length)
		if length > oldLength {
			panic(fmt.Sprintf("Target wem at index %d (%d bytes) is larger than the "+
				"original wem (%d bytes).\nUsing target wems that are larger than "+
				"the original wem is not yet supported", r.WemIndex, length, oldLength))
		}
		diff := oldLength - length
		wem.Reader = io.NewSectionReader(r.Wem, 0, length)
		remaining := int64(diff) + wem.RemainingLength
		wem.RemainingReader = io.NewSectionReader(&InfiniteReaderAt{0}, 0, remaining)

		oldDesc := wem.Descriptor
		desc := WemDescriptor{oldDesc.WemId, oldDesc.Offset, uint32(length)}
		wem.Descriptor = desc
		bnk.IndexSection.DescriptorMap[desc.WemId] = desc
	}
}

func (bnk *File) String() string {
	b := new(strings.Builder)

	b.WriteString(bnk.BankHeaderSection.String())
	b.WriteString(bnk.IndexSection.String())
	b.WriteString(bnk.DataSection.String())

	for _, sec := range bnk.Others {
		b.WriteString(sec.String())
	}

	return b.String()
}
