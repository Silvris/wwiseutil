// Package bnk implements access to the Wwise SoundBank file format.
package bnk

import (
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"os"
	"strings"
)

// The number of bytes used to describe the header of a section.
const SECTION_HEADER_BYTES = 8

// The number of bytes used to describe the known portion of a BKHD section,
// excluding its own header.
const BKHD_SECTION_BYTES = 8

// The number of bytes used to describe a single data index
// entry (a WemDescriptor) within the DIDX section.
const DIDX_ENTRY_BYTES = 12

// The identifier for the start of the BKHD (Bank Header) section.
var bkhdHeaderId = [4]byte{'B', 'K', 'H', 'D'}

// The identifier for the start of the DIDX (Data Index) section.
var didxHeaderId = [4]byte{'D', 'I', 'D', 'X'}

// The identifier for the start of the DATA section.
var dataHeaderId = [4]byte{'D', 'A', 'T', 'A'}

// A File represents an open Wwise SoundBank.
type File struct {
	closer            io.Closer
	BankHeaderSection *BankHeaderSection
	IndexSection      *DataIndexSection
	DataSection       *DataSection
	Others            []*UnknownSection
}

// A SectionHeader represents a single Wwise SoundBank header.
type SectionHeader struct {
	Identifier [4]byte
	Length     uint32
}

// A BankHeaderSection represents the BKHD section of a SoundBank file.
type BankHeaderSection struct {
	Header          *SectionHeader
	Descriptor      BankDescriptor
	RemainingReader io.Reader
}

// A BankDescriptor provides metadata about the overall SoundBank file.
type BankDescriptor struct {
	Version uint32
	BankId  uint32
}

// A DataIndexSection represents the DIDX section of a SoundBank file.
type DataIndexSection struct {
	Header *SectionHeader
	// The count of wems in this SoundBank.
	WemCount uint32
	// A list of all wem IDs, in order of their offset into the file.
	WemIds []uint32
	// A mapping from wem ID to its descriptor.
	DescriptorMap map[uint32]WemDescriptor
}

// A DataIndexSection represents the DATA section of a SoundBank file.
type DataSection struct {
	Header *SectionHeader
	// The offset into the file where the data portion of the DATA section begins.
	// This is the location where wem entries are stored.
	DataStart uint32
	Wems      []*Wem
}

// A Wem represents a single sound entity contained within a SoundBank file.
type Wem struct {
	io.Reader
	Descriptor WemDescriptor
	// A reader over the bytes that remain until the next wem if there is one, or
	// the end of the data section. These bytes are generally NUL(0x00) padding.
	RemainingReader io.Reader
	// The number of bytes remaining until the next wem if there is one, or the
	// end of the data section.
	RemainingLength int64
}

// A WemDescriptor represents the location of a single wem entity within the
// SoundBank DATA section.
type WemDescriptor struct {
	WemId uint32
	// The number of bytes from the start of the DATA section's data (after the
	// header and length) that this wem begins.
	Offset uint32
	// The length in bytes of this wem.
	Length uint32
}

// An UnknownSection represents an unknown section in a SoundBank file.
type UnknownSection struct {
	Header *SectionHeader
	// A reader to read the data of this section.
	Reader io.Reader
}

// A utility ReaderAt that emits an infinite stream of a specific value.
type InfiniteReaderAt struct {
	// The value that this padding writer will write.
	Value byte
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

func (bnk *File) String() string {
	b := new(strings.Builder)

	// TODO: Turn these into String() for each type.
	hdr := bnk.BankHeaderSection
	fmt.Fprintf(b, "%s: len(%d) version(%d) id(%d)\n", hdr.Header.Identifier,
		hdr.Header.Length, hdr.Descriptor.Version, hdr.Descriptor.BankId)

	idx := bnk.IndexSection
	total := uint32(0)
	for _, desc := range idx.DescriptorMap {
		total += desc.Length
	}
	fmt.Fprintf(b, "%s: len(%d) wem_count(%d)\n", idx.Header.Identifier,
		idx.Header.Length, idx.WemCount)
	fmt.Fprintf(b, "DIDX WEM total size: %d\n", total)

	data := bnk.DataSection
	fmt.Fprintf(b, "%s: len(%d)\n", data.Header.Identifier, data.Header.Length)

	for _, sec := range bnk.Others {
		fmt.Fprintf(b, "%s: len(%d)\n", sec.Header.Identifier, sec.Header.Length)
	}

	return b.String()
}

// NewBankHeaderSection creates a new BankHeaderSection, reading from sr, which
// must be seeked to the start of the BKHD section data.
// It is an error to call this method on a non-BKHD header.
func (hdr *SectionHeader) NewBankHeaderSection(sr *io.SectionReader) (*BankHeaderSection, error) {
	if hdr.Identifier != bkhdHeaderId {
		panic(fmt.Sprintf("Expected BKHD header but got: %s", hdr.Identifier))
	}
	sec := new(BankHeaderSection)
	sec.Header = hdr
	desc := BankDescriptor{}
	err := binary.Read(sr, binary.LittleEndian, &desc)
	if err != nil {
		return nil, err
	}
	sec.Descriptor = desc
	// Get the offset into the file where the known portion of the BKHD ends.
	knownOffset, _ := sr.Seek(0, io.SeekCurrent)
	remaining := int64(hdr.Length - BKHD_SECTION_BYTES)
	sec.RemainingReader = io.NewSectionReader(sr, knownOffset, remaining)
	sr.Seek(remaining, io.SeekCurrent)

	return sec, nil
}

// WriteTo writes the full contents of this BankHeaderSection to the Writer
// specified by w.
func (hdr *BankHeaderSection) WriteTo(w io.Writer) (written int64, err error) {
	err = binary.Write(w, binary.LittleEndian, hdr.Header)
	if err != nil {
		return
	}
	written = int64(SECTION_HEADER_BYTES)
	err = binary.Write(w, binary.LittleEndian, hdr.Descriptor)
	if err != nil {
		return
	}
	written += int64(BKHD_SECTION_BYTES)
	n, err := io.Copy(w, hdr.RemainingReader)
	if err != nil {
		return
	}
	written += int64(n)
	return written, nil
}

// NewDataIndexSection creates a new DataIndexSection, reading from r, which must
// be seeked to the start of the DIDX section data.
// It is an error to call this method on a non-DIDX header.
func (hdr *SectionHeader) NewDataIndexSection(r io.Reader) (*DataIndexSection, error) {
	if hdr.Identifier != didxHeaderId {
		panic(fmt.Sprintf("Expected DIDX header but got: %s", hdr.Identifier))
	}
	wemCount := hdr.Length / DIDX_ENTRY_BYTES
	sec := DataIndexSection{hdr, wemCount, make([]uint32, 0),
		make(map[uint32]WemDescriptor)}
	for i := uint32(0); i < wemCount; i++ {
		var desc WemDescriptor
		err := binary.Read(r, binary.LittleEndian, &desc)
		if err != nil {
			return nil, err
		}

		if _, ok := sec.DescriptorMap[desc.WemId]; ok {
			panic(fmt.Sprintf(
				"%d is an illegal repeated wem ID in the DIDX", desc.WemId))
		}
		sec.WemIds = append(sec.WemIds, desc.WemId)
		sec.DescriptorMap[desc.WemId] = desc
	}

	return &sec, nil
}

// WriteTo writes the full contents of this DataIndexSection to the Writer
// specified by w.
func (idx *DataIndexSection) WriteTo(w io.Writer) (written int64, err error) {
	err = binary.Write(w, binary.LittleEndian, idx.Header)
	if err != nil {
		return
	}
	written = int64(SECTION_HEADER_BYTES)

	for _, id := range idx.WemIds {
		desc := idx.DescriptorMap[id]
		err = binary.Write(w, binary.LittleEndian, desc)
		if err != nil {
			return
		}
		written += int64(DIDX_ENTRY_BYTES)
	}
	return written, nil
}

// NewDataSection creates a new DataSection, reading from sr, which must be
// seeked to the start of the DATA section data. idx specifies how each wem
// should be indexed from, given the current sr offset.
// It is an error to call this method on a non-DATA header.
func (hdr *SectionHeader) NewDataSection(sr *io.SectionReader,
	idx *DataIndexSection) (*DataSection, error) {
	if hdr.Identifier != dataHeaderId {
		panic(fmt.Sprintf("Expected DATA header but got: %s", hdr.Identifier))
	}
	dataOffset, _ := sr.Seek(0, io.SeekCurrent)

	sec := DataSection{hdr, uint32(dataOffset), make([]*Wem, 0)}
	for i, id := range idx.WemIds {
		desc := idx.DescriptorMap[id]
		wemStartOffset := dataOffset + int64(desc.Offset)
		wemReader := io.NewSectionReader(sr, wemStartOffset, int64(desc.Length))

		var remReader io.Reader
		remaining := int64(0)

		if i <= len(idx.WemIds)-1 {
			wemEndOffset := wemStartOffset + int64(desc.Length)
			var nextOffset int64
			if i == len(idx.WemIds)-1 {
				// This is the last wem, check how many bytes remain until the end of
				// the data section.
				nextOffset = dataOffset + int64(hdr.Length)
			} else {
				// This is not the last wem, check how many bytes remain until the next
				// wem.
				nextDesc := idx.DescriptorMap[idx.WemIds[i+1]]
				nextOffset = dataOffset + int64(nextDesc.Offset)
			}
			remaining = nextOffset - wemEndOffset
			// Pass a Reader over the remaining section if we have remaining bytes to
			// read, or an empty Reader if remaining is 0 (no bytes will be read).
			remReader = io.NewSectionReader(sr, wemEndOffset, remaining)
		}

		wem := Wem{wemReader, desc, remReader, remaining}
		sec.Wems = append(sec.Wems, &wem)
	}

	sr.Seek(int64(hdr.Length), io.SeekCurrent)
	return &sec, nil
}

// ReadAt fills all of len(p) bytes with the Value of this InfiniteReaderAt.
func (r *InfiniteReaderAt) ReadAt(p []byte, off int64) (int, error) {
	for i, _ := range p {
		p[i] = r.Value
	}
	return 1, nil
}

// ReplaceWem replaces the wem of File at index i, reading the wem, with
// specified length in from r.
func (bnk *File) ReplaceWem(i int, r io.ReaderAt, length int64) {
	wem := bnk.DataSection.Wems[i]
	oldLength := int64(wem.Descriptor.Length)
	if length > oldLength {
		panic("Replacing target wems that are larger than the original wems is " +
			"not yet supported")
	}
	diff := oldLength - length
	wem.Reader = io.NewSectionReader(r, 0, length)
	remaining := int64(diff) + wem.RemainingLength
	wem.RemainingReader = io.NewSectionReader(&InfiniteReaderAt{0}, 0, remaining)

	oldDesc := wem.Descriptor
	desc := WemDescriptor{oldDesc.WemId, oldDesc.Offset, uint32(length)}
	wem.Descriptor = desc
	bnk.IndexSection.DescriptorMap[desc.WemId] = desc
}

// WriteTo writes the full contents of this DataSection to the Writer specified
// by w.
func (data *DataSection) WriteTo(w io.Writer) (written int64, err error) {
	err = binary.Write(w, binary.LittleEndian, data.Header)
	if err != nil {
		return
	}
	written = int64(SECTION_HEADER_BYTES)
	for _, wem := range data.Wems {
		n, err := io.Copy(w, wem)
		if err != nil {
			return written, err
		}
		written += int64(n)
		n, err = io.Copy(w, wem.RemainingReader)
		if err != nil {
			return written, err
		}
		written += int64(n)
	}

	return written, nil
}

// NewUnknownSection creates a new UnknownSection, reading from sr, which
// must be seeked to the start of the unknown section data.
func (hdr *SectionHeader) NewUnknownSection(sr *io.SectionReader) (*UnknownSection, error) {
	// Get the offset into the file where the data portion of this section begins.
	dataOffset, _ := sr.Seek(0, io.SeekCurrent)
	r := io.NewSectionReader(sr, dataOffset, int64(hdr.Length))
	sr.Seek(int64(hdr.Length), io.SeekCurrent)
	return &UnknownSection{hdr, r}, nil
}

// WriteTo writes the full contents of this UnknownSection to the Writer
// specified by w.
func (unknown *UnknownSection) WriteTo(w io.Writer) (written int64, err error) {
	err = binary.Write(w, binary.LittleEndian, unknown.Header)
	if err != nil {
		return
	}
	written = int64(SECTION_HEADER_BYTES)

	n, err := io.Copy(w, unknown.Reader)
	if err != nil {
		return written, err
	}
	written += int64(n)

	return written, nil
}
