package main

import (
	"bytes"
	"encoding/binary"
	"flag"
	"fmt"
	"hash/crc32"
	"io"
	"log"
	"os"
	"strings"
	"unsafe"
)

//go:generate /bin/bash ./gen_ext4_defs/build.sh

const (
	SuperBlock0Offset = int64(1024)
)

/*
 * ext4DirEntry_2 requires manual parsing because of its size is based on the file name length
 * Thus we manually redefine this as a go struct
 */
type ext4DirEntry2Go struct {
	Inode    uint32
	RecLen   uint16
	NameLen  uint8
	FileType uint8
	Name     string
}

type Ext4FS struct {
	dev           *os.File
	superBlock    ext4SuperBlock
	blockSize     int64
	groupDescSize int64 /* Actually uint16 */

}

func check(e error) {
	if e != nil {
		panic(e)
	}
}

func calcChecksum(prefix []byte, data interface{}, upto uintptr, initialValue uint32) uint32 {
	var buf bytes.Buffer
	buf.Write(prefix)
	err := binary.Write(&buf, binary.LittleEndian, data)
	check(err)
	buf.Truncate(len(prefix) + int(upto))
	var calculated = initialValue
	calculated ^= crc32.Checksum(buf.Bytes(), crc32.MakeTable(crc32.Castagnoli))
	return calculated
}

func readSuperBlock(f *os.File) *Ext4FS {
	result := new(Ext4FS)
	result.dev = f

	/* We only support little endian and only attempt to read the first copy of the super block */
	_, err := f.Seek(SuperBlock0Offset, io.SeekStart)
	check(err)
	err = binary.Read(f, binary.LittleEndian, &result.superBlock)
	check(err)
	if result.superBlock.Magic != EXT4_SUPER_MAGIC {
		log.Panic("Ext4Magic not found")
	}

	switch result.superBlock.Checksum_type {
	case 0:
	/* Do nothing */
	case 1:
		calculated := calcChecksum(nil, result.superBlock, unsafe.Offsetof(result.superBlock.Checksum), 0xffffffff)
		if calculated != result.superBlock.Checksum {
			log.Panic("SuperBlock checksum incorrect")
		}
	default:
		_, _ = fmt.Fprintf(os.Stderr, "Unsupported superBlock checksum")
	}
	_, _ = fmt.Fprintf(os.Stderr, "%+v\n", result.superBlock)

	/* TODO: check (in)compat flags */

	result.blockSize = int64(BLOCK_SIZE << (result.superBlock.Log_block_size))

	result.groupDescSize = int64(32)
	if (result.superBlock.Feature_incompat & EXT4_FEATURE_INCOMPAT_64BIT) > 0 {
		result.groupDescSize = int64(result.superBlock.Desc_size)
	}
	/* TODO: Do a proper check ignoring and checking each flag */
	expectedFlags := 0
	expectedFlags |= EXT4_FEATURE_INCOMPAT_FILETYPE /* Expect ext4_dir_entry_2  as directory entries*/
	expectedFlags |= EXT4_FEATURE_INCOMPAT_RECOVER  /* Ignore unsafely unmounted filesystems */
	expectedFlags |= EXT4_FEATURE_INCOMPAT_EXTENTS  /* Assume we use extents */
	expectedFlags |= EXT4_FEATURE_INCOMPAT_64BIT    /* Explicitly supported */
	expectedFlags |= EXT4_FEATURE_INCOMPAT_MMP      /* Ignore multi mount protection */
	expectedFlags |= EXT4_FEATURE_INCOMPAT_FLEX_BG
	if (result.superBlock.Feature_incompat & (0xffffffff ^ uint32(expectedFlags))) > 0 {
		log.Panicf("Incompatible feature detected %x", result.superBlock.Feature_incompat)
	}
	return result
}

func readGroupDesc(ext4fs *Ext4FS, blockGroupNum int64) *ext4GroupDesc {
	gdtLocation := 1024/ext4fs.blockSize + 1
	addr := gdtLocation*ext4fs.blockSize + ext4fs.groupDescSize*blockGroupNum
	_, err := ext4fs.dev.Seek(addr, io.SeekStart)
	check(err)
	groupDesc := new(ext4GroupDesc)
	err = binary.Read(ext4fs.dev, binary.LittleEndian, groupDesc)
	check(err)
	/* TODO: validate groupDesc checksum */
	_, _ = fmt.Fprintf(os.Stderr, "%+v\n", groupDesc)
	return groupDesc
}

func readInode(fs *Ext4FS, inodeNr int64) *ext4Inode {
	groupDescIdx := (inodeNr - 1) / int64(fs.superBlock.Inodes_per_group)
	groupDesc := readGroupDesc(fs, groupDescIdx)
	inodeTableLoc := int64(groupDesc.Inode_table_lo) + (int64(groupDesc.Inode_table_hi) << 32)
	inodeIdx := (inodeNr - 1) % int64(fs.superBlock.Inodes_per_group)
	pos := inodeTableLoc*fs.blockSize + inodeIdx*int64(fs.superBlock.Inode_size)
	_, err := fs.dev.Seek(pos, io.SeekStart)
	check(err)
	inode := new(ext4Inode)
	err = binary.Read(fs.dev, binary.LittleEndian, inode)
	check(err)
	/* TODO: validate inode checksum */
	return inode
}

type Ext4InodeReader struct {
	fs     *Ext4FS
	inode  *ext4Inode
	offset int64
}

func NewExt4InodeReader(fs *Ext4FS, inodeNr int64) *Ext4InodeReader {
	result := new(Ext4InodeReader)
	result.fs = fs
	result.offset = 0
	result.inode = readInode(fs, inodeNr)

	if result.inode.Flags&EXT4_EXTENTS_FL == 0 {
		log.Panic("extent flag not set")
	}
	return result
}

func (inode *Ext4InodeReader) physicalLoc(blockOffset int64, input io.Reader) ext4Extent {
	if input == nil {
		buf := new(bytes.Buffer)
		err := binary.Write(buf, binary.LittleEndian, inode.inode.Block)
		input = buf
		check(err)
	}
	extentHeader := new(ext4ExtentHeader)
	err := binary.Read(input, binary.LittleEndian, extentHeader)
	check(err)
	_, _ = fmt.Fprintf(os.Stderr, "%+v\n", extentHeader)
	if extentHeader.Magic != EXT4_EXT_MAGIC {
		log.Panic("Extent header magic not found")
	}
	if extentHeader.Depth > 0 {
		extentIdxes := make([]ext4ExtentIdx, extentHeader.Entries)
		err = binary.Read(input, binary.LittleEndian, extentIdxes)
		_, _ = fmt.Fprintf(os.Stderr, "%+v\n", extentIdxes)
		check(err)
		var best *ext4ExtentIdx
		for _, eln := range extentIdxes {
			if int64(eln.Block) <= blockOffset {
				best = &eln
			}
		}
		if best == nil {
			log.Panic("Could not find extent")
		}
		leaf := inode.fs.blockSize * (int64(best.Leaf_lo) + int64(best.Leaf_hi)<<32)
		_, err = inode.fs.dev.Seek(leaf, io.SeekStart)
		check(err)
		return inode.physicalLoc(blockOffset, inode.fs.dev)
	} else { /* depth == 0 */
		leafNodes := make([]ext4Extent, extentHeader.Entries)
		err := binary.Read(input, binary.LittleEndian, leafNodes)
		check(err)
		_, _ = fmt.Fprintf(os.Stderr, "%+v\n", leafNodes)
		for _, eln := range leafNodes {
			if (int64(eln.Block) <= blockOffset) && (blockOffset < int64(eln.Block)+int64(eln.Len)) {
				if eln.Block < 0 {
					log.Panic("non initialized blocks not supported")
				}
				return eln
			}
		}
	}
	log.Panic("Can not find extent for block offset")
	return ext4Extent{}
}

func (inode *Ext4InodeReader) read(size int64) []byte {
	fileSize := int64(inode.inode.Size_lo) + int64(inode.inode.Size_high)<<32
	if inode.offset == fileSize {
		return make([]byte, 0)
	}

	/* Read upto size bytes */
	blockOffset := inode.offset / inode.fs.blockSize
	extent := inode.physicalLoc(blockOffset, nil)
	if extent.Block < 0 {
		log.Panic("non initialized blocks not supported")
	}
	logicalStart := inode.fs.blockSize * int64(extent.Block)
	offsetIntoExtent := inode.offset - logicalStart
	physicalStart := inode.fs.blockSize * (int64(extent.Start_lo) + int64(extent.Start_hi)<<32)
	_, _ = fmt.Fprintf(os.Stderr, "Reading from %x %x %x\n", logicalStart, offsetIntoExtent, physicalStart)

	bytesToRead := inode.fs.blockSize*int64(extent.Len) - offsetIntoExtent
	if bytesToRead > size {
		bytesToRead = size
	}

	if fileSize < inode.offset+bytesToRead {
		bytesToRead = fileSize - inode.offset
	}

	_, err := inode.fs.dev.Seek(physicalStart+offsetIntoExtent, io.SeekStart)
	check(err)
	result := make([]byte, bytesToRead)
	_, err = inode.fs.dev.Read(result)
	check(err)
	inode.offset += int64(len(result))
	return result
}

func listDir(inode *Ext4InodeReader) []ext4DirEntry2Go {
	if inode.inode.Mode&S_IFDIR == 0 {
		log.Panic("inode is not a directory")
	}

	dirBlock := inode.read(inode.fs.blockSize)
	buf := bytes.NewReader(dirBlock)

	if inode.inode.Flags&EXT4_INDEX_FL > 0 {
		dxRoot_ := new(dxRoot)
		err := binary.Read(buf, binary.LittleEndian, dxRoot_)
		check(err)
		fmt.Fprintf(os.Stderr, "%+v\n", dxRoot_)
		dxCountlimit_ := new(dxCountlimit)
		err = binary.Read(buf, binary.LittleEndian, dxCountlimit_)
		check(err)
		fmt.Fprintf(os.Stderr, "%+v\n", dxCountlimit_)
		block := uint32(0)
		err = binary.Read(buf, binary.LittleEndian, &block)
		check(err)
		fmt.Fprintf(os.Stderr, "%+v\n", block)

		for i := uint16(1); i < dxCountlimit_.Count; i++ {
			dxEntry := new(dxEntry)
			err = binary.Read(buf, binary.LittleEndian, dxEntry)
			check(err)
			fmt.Fprintf(os.Stderr, "%+v\n", dxEntry)
		}

		if dxRoot_.Info.Indirect_levels > 0 {
			log.Panic("indirect_levels > 0 not implemented")
		}
	}

	var dirEntries []ext4DirEntry2Go
	for {
		if len(dirBlock) == 0 {
			return dirEntries
		}
		buf := bytes.NewReader(dirBlock)
		for buf.Len() > 0 {
			dirEntry := new(ext4DirEntry2Go)
			err := binary.Read(buf, binary.LittleEndian, &dirEntry.Inode)
			check(err)
			err = binary.Read(buf, binary.LittleEndian, &dirEntry.RecLen)
			/* Should use RecLen (and not NameLen) */
			check(err)
			err = binary.Read(buf, binary.LittleEndian, &dirEntry.NameLen)
			check(err)
			err = binary.Read(buf, binary.LittleEndian, &dirEntry.FileType)
			check(err)
			name := make([]byte, dirEntry.NameLen)
			err = binary.Read(buf, binary.LittleEndian, name)
			check(err)
			dirEntry.Name = string(name[:])
			_, _ = fmt.Fprintf(os.Stderr, "%+v\n", dirEntry)
			tmp := make([]byte, int(dirEntry.RecLen)-int(dirEntry.NameLen)-8)
			_, err = buf.Read(tmp)
			check(err)
			if dirEntry.Inode == 0 {
				break
			}
			dirEntries = append(dirEntries, *dirEntry)
		}
		dirBlock = inode.read(inode.fs.blockSize)
	}
	return dirEntries
}

func readFile(fsFile *os.File, filePath string, outFile io.Writer) {
	ext4fs := readSuperBlock(fsFile)

	inode := NewExt4InodeReader(ext4fs, int64(EXT4_ROOT_INO))

	if inode.inode.Mode&S_IFDIR == 0 {
		log.Panic("inode is not a directory")
	}

	pathElems := strings.Split(strings.TrimPrefix(filePath, "/"), "/")
	for _, pathElem := range pathElems {
		_, _ = fmt.Fprintf(os.Stderr, "%+v\n", inode.inode)

		if inode.inode.Mode&S_IFDIR == 0 {
			log.Panic("inode is not a directory")
		}

		nextInodeNr := uint32(0)
		dirEntries := listDir(inode)
		for _, entry := range dirEntries {
			if entry.Name == pathElem {
				nextInodeNr = entry.Inode
			}
		}

		if nextInodeNr == 0 {
			log.Panicf("%s not found", filePath)
		}

		inode = NewExt4InodeReader(ext4fs, int64(nextInodeNr))
	}

	for true {
		buf3 := inode.read(4096 * 10)
		if len(buf3) == 0 {
			break
		}
		_, _ = outFile.Write(buf3)
	}
}

func usage() {
	_, _ = fmt.Fprintf(os.Stderr, "Usage of %s:\n", os.Args[0])
	_, _ = fmt.Fprintf(os.Stderr, "  %s EXT4.IMG filename\n", os.Args[0])
	flag.PrintDefaults()
}

func main() {
	flag.Usage = usage
	flag.Parse()
	if flag.NArg() != 2 {
		usage()
		os.Exit(2)
	}

	filename := flag.Arg(0)
	filename2 := flag.Arg(1)
	f, err := os.Open(filename)
	check(err)

	readFile(f, filename2, os.Stdout)

	err = f.Close()
	check(err)

}
