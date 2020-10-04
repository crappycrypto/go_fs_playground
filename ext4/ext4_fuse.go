package go_fs_playground

import (
	"bazil.org/fuse"
	"bazil.org/fuse/fs"
	"context"
	"log"
	"os"
	"syscall"
	"time"
)

type Ext4Fuse struct {
	Ext4fs *Ext4FS
}

type Ext4FuseDir struct {
	ext4fs *Ext4FS
	inode  *Ext4InodeReader
}

type Ext4FuseFile struct {
	ext4fs *Ext4FS
	inode  *Ext4InodeReader
}

func (fs *Ext4Fuse) Root() (fs.Node, error) {
	inode := NewExt4InodeReader(fs.Ext4fs, int64(EXT4_ROOT_INO))
	if inode.inode.Mode&S_IFDIR == 0 {
		log.Panic("inode is not a directory")
	}
	return Ext4FuseDir{fs.Ext4fs, inode}, nil
}

func inodeAttr(inode *ext4Inode, attr *fuse.Attr) error {
	attr.Mode = os.FileMode(inode.Mode)
	if inode.Mode&S_IFDIR != 0 {
		attr.Mode = os.ModeDir | attr.Mode
	}
	attr.Atime = time.Unix((int64(inode.Atime_extra)&4)<<32+int64(inode.Atime), int64(inode.Atime_extra)>>2)
	attr.Mtime = time.Unix((int64(inode.Mtime_extra)&4)<<32+int64(inode.Mtime), int64(inode.Mtime_extra)>>2)
	attr.Ctime = time.Unix((int64(inode.Ctime_extra)&4)<<32+int64(inode.Ctime), int64(inode.Ctime_extra)>>2)
	attr.Uid = uint32(inode.Uid)
	attr.Gid = uint32(inode.Gid)
	attr.Size = uint64(inode.Size_lo) + uint64(inode.Size_high)<<32
	log.Printf("%+v\n", attr)
	return nil
}

func (dir Ext4FuseDir) Attr(ctx context.Context, attr *fuse.Attr) error {
	result := inodeAttr(dir.inode.inode, attr)
	return result
}

func (file Ext4FuseFile) Attr(ctx context.Context, attr *fuse.Attr) error {
	result := inodeAttr(file.inode.inode, attr)
	return result
}

func (dir Ext4FuseDir) Lookup(ctx context.Context, name string) (fs.Node, error) {
	log.Printf("Lookup %s \n", name)
	dirEntries := listDir(dir.inode)
	for _, entry := range dirEntries {
		if entry.Name == name {
			log.Printf("Found %+v\n", entry)
			return Ext4FuseFile{ext4fs: dir.ext4fs, inode: NewExt4InodeReader(dir.ext4fs, int64(entry.Inode))}, nil
		}
	}
	return nil, syscall.ENOENT
}

func (dir Ext4FuseDir) ReadDirAll(ctx context.Context) ([]fuse.Dirent, error) {
	var result []fuse.Dirent
	dirEntries := listDir(dir.inode)
	for _, entry := range dirEntries {
		fuseType := fuse.DT_Unknown
		if entry.FileType == DT_REG {
			fuseType = fuse.DT_File
		} else if entry.FileType == DT_DIR {
			fuseType = fuse.DT_Dir
		}
		result = append(result, fuse.Dirent{Inode: uint64(entry.Inode), Name: entry.Name, Type: fuseType})
	}
	log.Printf("%+v\n", result)
	return result, nil
}

func (file Ext4FuseFile) Open(ctx context.Context, req *fuse.OpenRequest, resp *fuse.OpenResponse) (fs.Handle, error) {
	log.Printf("Open %+v \n", file)
	if !req.Flags.IsReadOnly() {
		return nil, fuse.Errno(syscall.EACCES)
	}
	return file, nil
}

func (file Ext4FuseFile) Read(ctx context.Context, req *fuse.ReadRequest, resp *fuse.ReadResponse) error {
	file.inode.offset = req.Offset
	var result []byte
	for true {
		partial := file.inode.read(int64(req.Size - len(result)))
		if len(partial) == 0 {
			break
		}
		result = append(result, partial...)
	}
	resp.Data = result
	log.Printf("Read %+v offset %d size %d resp %d\n", file, req.Offset, req.Size, len(resp.Data))
	return nil
}
