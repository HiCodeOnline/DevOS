package dbapp

import (
	"reflect"
	"strings"
	"testing"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

func TestSplitRedisCommand(t *testing.T) {
	cases := []struct {
		line string
		want []string
	}{
		{`SET user:1 "Alice Smith"`, []string{"SET", "user:1", "Alice Smith"}},
		{`GET 'a b'`, []string{"GET", "a b"}},
		{`HSET h f "a\"b"`, []string{"HSET", "h", "f", `a"b`}},
		{``, nil},
	}
	for _, c := range cases {
		if got := splitRedisCommand(c.line); !reflect.DeepEqual(got, c.want) {
			t.Errorf("splitRedisCommand(%q) = %#v, want %#v", c.line, got, c.want)
		}
	}
}

func TestRedisIndexFallback(t *testing.T) {
	if got := redisIndex(Connection{Database: "3"}, ""); got != 3 {
		t.Fatalf("redisIndex = %d, want 3", got)
	}
	if got := redisIndex(Connection{Database: "abc"}, ""); got != 0 {
		t.Fatalf("redisIndex = %d, want 0", got)
	}
	if got := redisIndex(Connection{Database: "1"}, "5"); got != 5 {
		t.Fatalf("redisIndex = %d, want 5", got)
	}
}

func TestMongoURI(t *testing.T) {
	uri := mongoURI(Connection{ID: "x", Driver: "mongodb", Host: "db.local", Port: 27018, User: "admin", Password: "p@ss", Params: "authSource=admin&replicaSet=rs0"})
	for _, want := range []string{"mongodb://admin:p%40ss@db.local:27018", "authSource=admin", "replicaSet=rs0"} {
		if !strings.Contains(uri, want) {
			t.Errorf("mongoURI = %q, 缺少 %q", uri, want)
		}
	}
	if got := mongoURI(Connection{ID: "x", Driver: "mongodb", Host: "127.0.0.1"}); got != "mongodb://127.0.0.1:27017" {
		t.Errorf("mongoURI default = %q", got)
	}
}

func TestMongoTableColumnsUnion(t *testing.T) {
	docs := []bson.D{
		{{Key: "_id", Value: primitive.NewObjectID()}, {Key: "name", Value: "a"}},
		{{Key: "_id", Value: primitive.NewObjectID()}, {Key: "age", Value: 3}},
	}
	cols, rows := mongoTable(docs)
	if !reflect.DeepEqual(cols, []string{"_id", "name", "age"}) {
		t.Fatalf("columns = %#v", cols)
	}
	if len(rows) != 2 || rows[1][2] != 3 {
		t.Fatalf("rows = %#v", rows)
	}
}

func TestMongoValueConversion(t *testing.T) {
	oid := primitive.NewObjectID()
	if got, ok := mongoValue(oid).(string); !ok || got != oid.Hex() {
		t.Fatalf("ObjectID -> %#v", got)
	}
	doc, ok := mongoValue(bson.D{{Key: "n", Value: int64(1)}}).(map[string]any)
	if !ok || doc["n"] != int64(1) {
		t.Fatalf("bson.D -> %#v", doc)
	}
	if v := mongoValue(primitive.Decimal128{}); v == nil {
		t.Fatalf("Decimal128 不应为空")
	}
}
