package blob

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
)

const localMagic = "SIBLOB01"

// JSON escaping can expand each of the 4096 content-type bytes sixfold.
const maxHeaderSize = 32 * 1024

type localStore struct{ root *os.Root }

// NewLocal anchors all operations to root, even if its parent is later renamed.
// Payload and metadata share one file, atomically renamed after validation.
func NewLocal(root string) (Store, error) {
	if err := os.MkdirAll(root, 0700); err != nil {
		return nil, ErrStorage
	}
	dir, err := os.OpenRoot(root)
	if err != nil {
		return nil, ErrStorage
	}
	return &localStore{root: dir}, nil
}

func (s *localStore) Put(ctx context.Context, key string, body io.Reader, size int64, contentType string) error {
	if err := validatePut(key, body, size, contentType); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return ErrStorage
	}
	name := ".stage-" + hex.EncodeToString(random[:])
	file, err := s.root.OpenFile(name, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return ErrStorage
	}
	defer s.root.Remove(name)
	defer file.Close()
	metadata := Metadata{Size: size, ContentType: contentType, ETag: key}
	header, err := json.Marshal(metadata)
	if err != nil {
		return ErrStorage
	}
	prefix := make([]byte, len(localMagic)+4)
	copy(prefix, localMagic)
	binary.BigEndian.PutUint32(prefix[len(localMagic):], uint32(len(header)))
	if _, err = file.Write(prefix); err != nil {
		return ErrStorage
	}
	if _, err = file.Write(header); err != nil {
		return ErrStorage
	}
	if err := copyVerified(ctx, file, body, size, key); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return ErrStorage
	}
	if err := file.Close(); err != nil {
		return ErrStorage
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	unlock, err := lockLocalMutation(ctx, s.root)
	if err != nil {
		return err
	}
	defer unlock()
	if err := s.root.Rename(name, key); err != nil {
		return ErrStorage
	}
	return nil
}

// openRecord rejects links and special files, bounds header decoding, and pins
// one inode so a concurrent Put cannot mix an old header with new bytes. Root
// enforces containment even if a path becomes a symlink after Lstat; comparing
// inode identities would incorrectly reject concurrent atomic replacement.
func (s *localStore) openRecord(key string) (*os.File, Metadata, int64, error) {
	before, err := s.root.Lstat(key)
	if err != nil {
		return nil, Metadata{}, 0, localError(err)
	}
	if !before.Mode().IsRegular() {
		return nil, Metadata{}, 0, ErrStorage
	}
	file, err := s.root.Open(key)
	if err != nil {
		return nil, Metadata{}, 0, localError(err)
	}
	failed := true
	defer func() {
		if failed {
			file.Close()
		}
	}()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return nil, Metadata{}, 0, ErrStorage
	}
	var prefix [len(localMagic) + 4]byte
	if _, err := io.ReadFull(file, prefix[:]); err != nil || string(prefix[:len(localMagic)]) != localMagic {
		return nil, Metadata{}, 0, ErrStorage
	}
	size := binary.BigEndian.Uint32(prefix[len(localMagic):])
	if size == 0 || size > maxHeaderSize {
		return nil, Metadata{}, 0, ErrStorage
	}
	header := make([]byte, size)
	if _, err := io.ReadFull(file, header); err != nil {
		return nil, Metadata{}, 0, ErrStorage
	}
	var metadata Metadata
	if err := json.Unmarshal(header, &metadata); err != nil {
		return nil, Metadata{}, 0, ErrStorage
	}
	offset := int64(len(prefix)) + int64(size)
	if metadata.Size < 0 || metadata.Size > MaxSize || metadata.ETag != key || validateContentType(metadata.ContentType) != nil || info.Size() != offset+metadata.Size {
		return nil, Metadata{}, 0, ErrStorage
	}
	failed = false
	return file, metadata, offset, nil
}

func (s *localStore) Stat(ctx context.Context, key string) (Metadata, error) {
	if err := validateKey(key); err != nil {
		return Metadata{}, err
	}
	if err := ctx.Err(); err != nil {
		return Metadata{}, err
	}
	file, metadata, _, err := s.openRecord(key)
	if err != nil {
		return Metadata{}, err
	}
	file.Close()
	if err := ctx.Err(); err != nil {
		return Metadata{}, err
	}
	return metadata, nil
}

func (s *localStore) Get(ctx context.Context, key string, offset, length int64) (io.ReadCloser, error) {
	if err := validateKey(key); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	file, metadata, start, err := s.openRecord(key)
	if err != nil {
		return nil, err
	}
	count, err := rangeLength(metadata.Size, offset, length)
	if err != nil {
		file.Close()
		return nil, err
	}
	return &objectReader{ctx: ctx, reader: io.NewSectionReader(file, start+offset, count), closer: file}, nil
}

func (s *localStore) Delete(ctx context.Context, key string) error {
	return s.DeleteGuarded(ctx, key, nil)
}

func (s *localStore) DeleteGuarded(ctx context.Context, key string, guard func(context.Context) error) error {
	if err := validateKey(key); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	unlock, err := lockLocalMutation(ctx, s.root)
	if err != nil {
		return err
	}
	defer unlock()
	if guard != nil {
		if err = guard(ctx); err != nil {
			return err
		}
	}
	info, err := s.root.Lstat(key)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil || info.IsDir() {
		return ErrStorage
	}
	if err := s.root.Remove(key); err != nil && !errors.Is(err, os.ErrNotExist) {
		return ErrStorage
	}
	return nil
}

func localError(err error) error {
	if errors.Is(err, os.ErrNotExist) {
		return ErrNotFound
	}
	return ErrStorage
}
