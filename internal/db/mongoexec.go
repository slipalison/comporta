package db

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/slipalison/comporta/internal/model"
)

// ParseDoc reads a MongoDB Extended JSON object. Empty input is an empty
// document.
func ParseDoc(raw json.RawMessage) (bson.D, error) {
	doc := bson.D{}
	s := strings.TrimSpace(string(raw))
	if s == "" || s == "null" {
		return doc, nil
	}
	if err := bson.UnmarshalExtJSON([]byte(s), false, &doc); err != nil {
		return nil, fmt.Errorf("invalid Extended JSON object: %w", err)
	}
	return doc, nil
}

func ParseDocs(raw json.RawMessage) ([]bson.D, error) {
	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, fmt.Errorf("expected a JSON array of objects: %w", err)
	}
	docs := make([]bson.D, 0, len(items))
	for i, item := range items {
		d, err := ParseDoc(item)
		if err != nil {
			return nil, fmt.Errorf("item %d: %w", i, err)
		}
		docs = append(docs, d)
	}
	return docs, nil
}

// PipelineOps returns the permissions an aggregation pipeline needs: $merge
// writes into a collection and $out replaces one.
func PipelineOps(pipeline []bson.D) []model.Op {
	ops := map[model.Op]bool{}
	for _, stage := range pipeline {
		for _, e := range stage {
			switch e.Key {
			case "$merge":
				ops[model.OpInsert], ops[model.OpUpdate] = true, true
			case "$out":
				ops[model.OpInsert], ops[model.OpDelete], ops[model.OpDDL] = true, true, true
			}
		}
	}
	var list []model.Op
	for _, op := range []model.Op{model.OpInsert, model.OpUpdate, model.OpDelete, model.OpDDL} {
		if ops[op] {
			list = append(list, op)
		}
	}
	return list
}

type MongoResult struct {
	Documents []json.RawMessage `json:"documents"`
	Count     int               `json:"count"`
	Truncated bool              `json:"truncated,omitempty"`
}

func readCursor(ctx context.Context, cur *mongo.Cursor, maxRows int) (*MongoResult, error) {
	defer func() { _ = cur.Close(ctx) }()
	res := &MongoResult{Documents: []json.RawMessage{}}
	for cur.Next(ctx) {
		if len(res.Documents) >= maxRows {
			res.Truncated = true
			break
		}
		b, err := bson.MarshalExtJSON(cur.Current, false, false)
		if err != nil {
			return nil, err
		}
		if len(b) > MaxCellChars*4 {
			b, _ = json.Marshal(map[string]string{"_truncated": truncate(string(b))})
		}
		res.Documents = append(res.Documents, b)
	}
	if err := cur.Err(); err != nil {
		return nil, err
	}
	res.Count = len(res.Documents)
	return res, nil
}

type FindRequest struct {
	Filter, Projection, Sort bson.D
	Skip                     int64
	MaxRows                  int
}

func MongoFind(ctx context.Context, coll *mongo.Collection, req FindRequest) (*MongoResult, error) {
	opts := options.Find().SetLimit(int64(req.MaxRows) + 1)
	if len(req.Projection) > 0 {
		opts.SetProjection(req.Projection)
	}
	if len(req.Sort) > 0 {
		opts.SetSort(req.Sort)
	}
	if req.Skip > 0 {
		opts.SetSkip(req.Skip)
	}
	cur, err := coll.Find(ctx, req.Filter, opts)
	if err != nil {
		return nil, err
	}
	return readCursor(ctx, cur, req.MaxRows)
}

func MongoAggregate(ctx context.Context, coll *mongo.Collection, pipeline []bson.D, maxRows int) (*MongoResult, error) {
	cur, err := coll.Aggregate(ctx, pipeline)
	if err != nil {
		return nil, err
	}
	return readCursor(ctx, cur, maxRows)
}

type FieldInfo struct {
	Path  string   `json:"path"`
	Types []string `json:"types"`
	Seen  int      `json:"seen_in"`
}

type CollectionInfo struct {
	Collection string            `json:"collection"`
	Sampled    int               `json:"sampled_documents"`
	Estimated  int64             `json:"estimated_document_count"`
	Fields     []FieldInfo       `json:"fields"`
	Indexes    []json.RawMessage `json:"indexes"`
}

// DescribeCollection infers field paths and types from a random sample.
func DescribeCollection(ctx context.Context, coll *mongo.Collection, sampleSize int) (*CollectionInfo, error) {
	info := &CollectionInfo{Collection: coll.Name(), Fields: []FieldInfo{}, Indexes: []json.RawMessage{}}
	info.Estimated, _ = coll.EstimatedDocumentCount(ctx)

	cur, err := coll.Aggregate(ctx, bson.A{bson.D{{Key: "$sample", Value: bson.D{{Key: "size", Value: sampleSize}}}}})
	if err != nil {
		return nil, err
	}
	defer func() { _ = cur.Close(ctx) }()
	fields := map[string]*FieldInfo{}
	for cur.Next(ctx) {
		info.Sampled++
		walk(cur.Current, "", 0, fields)
	}
	if err := cur.Err(); err != nil {
		return nil, err
	}
	for _, f := range fields {
		sort.Strings(f.Types)
		info.Fields = append(info.Fields, *f)
	}
	sort.Slice(info.Fields, func(i, j int) bool { return info.Fields[i].Path < info.Fields[j].Path })

	icur, err := coll.Indexes().List(ctx)
	if err == nil {
		defer func() { _ = icur.Close(ctx) }()
		for icur.Next(ctx) {
			if b, err := bson.MarshalExtJSON(icur.Current, false, false); err == nil {
				info.Indexes = append(info.Indexes, b)
			}
		}
	}
	return info, nil
}

func walk(doc bson.Raw, prefix string, depth int, fields map[string]*FieldInfo) {
	elems, err := doc.Elements()
	if err != nil {
		return
	}
	for _, e := range elems {
		path := prefix + e.Key()
		v := e.Value()
		f, ok := fields[path]
		if !ok {
			f = &FieldInfo{Path: path}
			fields[path] = f
		}
		f.Seen++
		t := v.Type.String()
		if !slices.Contains(f.Types, t) {
			f.Types = append(f.Types, t)
		}
		if sub, ok := v.DocumentOK(); ok && depth < 2 {
			walk(sub, path+".", depth+1, fields)
		}
	}
}

var ErrEmptyFilter = errors.New("refusing to touch every document with an empty filter; pass a filter or set all_documents=true")
