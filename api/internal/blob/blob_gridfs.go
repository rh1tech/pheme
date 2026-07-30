package blob

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/gridfs"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// GridFS stores blobs in MongoDB GridFS. It opens its own connection so the
// driver stays self-contained and decoupled from the document Store.
type GridFS struct {
	client *mongo.Client
	db     *mongo.Database
}

// NewGridFS connects to MongoDB and prepares a GridFS bucket on the database.
func NewGridFS(ctx context.Context, uri, dbName string) (*GridFS, error) {
	client, err := mongo.Connect(ctx, options.Client().ApplyURI(uri))
	if err != nil {
		return nil, err
	}
	if err := client.Ping(ctx, nil); err != nil {
		_ = client.Disconnect(ctx)
		return nil, err
	}
	db := client.Database(dbName)
	// Prove the bucket can be built now rather than failing on the first upload.
	if _, err := gridfs.NewBucket(db); err != nil {
		_ = client.Disconnect(ctx)
		return nil, err
	}
	return &GridFS{client: client, db: db}, nil
}

// Put uploads data under a new id, recording the content type in file metadata.
func (g *GridFS) Put(ctx context.Context, data []byte, contentType string) (string, error) {
	b, err := g.bucket()
	if err != nil {
		return "", err
	}
	id, err := newID()
	if err != nil {
		return "", err
	}
	opts := options.GridFSUpload().SetMetadata(bson.M{"contentType": contentType})
	if err := b.UploadFromStreamWithID(id, id, bytes.NewReader(data), opts); err != nil {
		return "", err
	}
	return id, nil
}

// bucket returns a Bucket for ONE operation.
//
// The driver's Bucket is not safe for concurrent use. It mutates itself on the first write to
// record that it has created its indexes, and it reuses an internal buffer across uploads — the
// race detector catches both, and this app uploads concurrently as a matter of course: several
// images in one message, several people at once. Sharing one Bucket meant two uploads could
// interleave into the same buffer, which is not a crash but a corrupted image.
//
// A Bucket is a thin handle over two collections, so making one per call is cheap, and it is the
// only way to use this driver concurrently without serialising every upload behind a mutex.
func (g *GridFS) bucket() (*gridfs.Bucket, error) { return gridfs.NewBucket(g.db) }

// Get downloads the bytes and content type for an id.
func (g *GridFS) Get(_ context.Context, id string) ([]byte, string, error) {
	b, err := g.bucket()
	if err != nil {
		return nil, "", err
	}
	ds, err := b.OpenDownloadStream(id)
	if err != nil {
		if errors.Is(err, gridfs.ErrFileNotFound) {
			return nil, "", ErrNotFound
		}
		return nil, "", err
	}
	defer func() { _ = ds.Close() }()

	contentType := "application/octet-stream"
	if file := ds.GetFile(); file != nil && len(file.Metadata) > 0 {
		var meta struct {
			ContentType string `bson:"contentType"`
		}
		if err := bson.Unmarshal(file.Metadata, &meta); err == nil && meta.ContentType != "" {
			contentType = meta.ContentType
		}
	}

	const maxBlobRead = 200 * 1024 * 1024 // 200 MiB — above the 128 MiB history cap
	reader := io.LimitReader(ds, maxBlobRead+1)
	data, err := io.ReadAll(reader)
	if err != nil {
		return nil, "", err
	}
	if int64(len(data)) > maxBlobRead {
		return nil, "", fmt.Errorf("blob exceeds maximum size")
	}
	return data, contentType, nil
}

// Delete removes a blob; a missing id is treated as success.
func (g *GridFS) Delete(_ context.Context, id string) error {
	b, err := g.bucket()
	if err != nil {
		return err
	}
	if err := b.Delete(id); err != nil && !errors.Is(err, gridfs.ErrFileNotFound) {
		return err
	}
	return nil
}

// Close disconnects the underlying client.
func (g *GridFS) Close(ctx context.Context) error {
	return g.client.Disconnect(ctx)
}
