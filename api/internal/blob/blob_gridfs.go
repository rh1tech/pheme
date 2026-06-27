package blob

import (
	"bytes"
	"context"
	"errors"
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
	bucket *gridfs.Bucket
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
	bucket, err := gridfs.NewBucket(client.Database(dbName))
	if err != nil {
		_ = client.Disconnect(ctx)
		return nil, err
	}
	return &GridFS{client: client, bucket: bucket}, nil
}

// Put uploads data under a new id, recording the content type in file metadata.
func (g *GridFS) Put(_ context.Context, data []byte, contentType string) (string, error) {
	id := newID()
	opts := options.GridFSUpload().SetMetadata(bson.M{"contentType": contentType})
	if err := g.bucket.UploadFromStreamWithID(id, id, bytes.NewReader(data), opts); err != nil {
		return "", err
	}
	return id, nil
}

// Get downloads the bytes and content type for an id.
func (g *GridFS) Get(_ context.Context, id string) ([]byte, string, error) {
	ds, err := g.bucket.OpenDownloadStream(id)
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

	data, err := io.ReadAll(ds)
	if err != nil {
		return nil, "", err
	}
	return data, contentType, nil
}

// Delete removes a blob; a missing id is treated as success.
func (g *GridFS) Delete(_ context.Context, id string) error {
	if err := g.bucket.Delete(id); err != nil && !errors.Is(err, gridfs.ErrFileNotFound) {
		return err
	}
	return nil
}

// Close disconnects the underlying client.
func (g *GridFS) Close(ctx context.Context) error {
	return g.client.Disconnect(ctx)
}
