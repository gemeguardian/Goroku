package goroku

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

type databaseSaver interface {
	Save() error
}

type databaseContextSaver interface {
	SaveContext(context.Context) error
}

var _ databaseSaver = (*Database)(nil)
var _ databaseContextSaver = (*Database)(nil)

type databaseRedisServer struct {
	listener   net.Listener
	setStarted chan struct{}
	releaseSet chan struct{}
	setOnce    sync.Once
	mu         sync.Mutex
	getValue   []byte
	lastSet    []byte
}

func newDatabaseRedisServer(t *testing.T, blockSet bool) *databaseRedisServer {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen for test Redis server: %v", err)
	}
	server := &databaseRedisServer{listener: listener}
	if blockSet {
		server.setStarted = make(chan struct{})
		server.releaseSet = make(chan struct{})
	}
	go server.serve()
	t.Cleanup(func() { _ = listener.Close() })
	return server
}

func (s *databaseRedisServer) serve() {
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			return
		}
		go s.serveConn(conn)
	}
}

func (s *databaseRedisServer) serveConn(conn net.Conn) {
	defer conn.Close()
	reader := bufio.NewReader(conn)
	for {
		args, err := readDatabaseRedisCommand(reader)
		if err != nil {
			return
		}
		command := strings.ToUpper(args[0])
		switch command {
		case "HELLO":
			_, _ = conn.Write([]byte("-ERR unknown command 'hello'\r\n"))
		case "GET":
			s.mu.Lock()
			value := append([]byte(nil), s.getValue...)
			s.mu.Unlock()
			if value == nil {
				_, _ = conn.Write([]byte("$-1\r\n"))
			} else {
				_, _ = fmt.Fprintf(conn, "$%d\r\n%s\r\n", len(value), value)
			}
		case "SET":
			if len(args) >= 3 {
				s.mu.Lock()
				s.lastSet = append(s.lastSet[:0], args[2]...)
				s.mu.Unlock()
			}
			if s.setStarted != nil {
				s.setOnce.Do(func() { close(s.setStarted) })
				<-s.releaseSet
			}
			_, _ = conn.Write([]byte("+OK\r\n"))
		default:
			_, _ = conn.Write([]byte("+OK\r\n"))
		}
	}
}

func (s *databaseRedisServer) setStoredValue(value []byte) {
	s.mu.Lock()
	s.getValue = append([]byte(nil), value...)
	s.mu.Unlock()
}

func (s *databaseRedisServer) lastStoredValue() []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]byte(nil), s.lastSet...)
}

func readDatabaseRedisCommand(reader *bufio.Reader) ([]string, error) {
	line, err := reader.ReadString('\n')
	if err != nil {
		return nil, err
	}
	line = strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
	if len(line) == 0 || line[0] != '*' {
		return nil, fmt.Errorf("invalid RESP array header %q", line)
	}
	count, err := strconv.Atoi(line[1:])
	if err != nil {
		return nil, err
	}
	args := make([]string, count)
	for i := range count {
		header, err := reader.ReadString('\n')
		if err != nil {
			return nil, err
		}
		header = strings.TrimSuffix(strings.TrimSuffix(header, "\n"), "\r")
		if len(header) == 0 || header[0] != '$' {
			return nil, fmt.Errorf("invalid RESP bulk header %q", header)
		}
		length, err := strconv.Atoi(header[1:])
		if err != nil {
			return nil, err
		}
		value := make([]byte, length)
		if _, err := io.ReadFull(reader, value); err != nil {
			return nil, err
		}
		if _, err := reader.Discard(2); err != nil {
			return nil, err
		}
		args[i] = string(value)
	}
	return args, nil
}

func newDatabaseRedisClient(server *databaseRedisServer) *redis.Client {
	return redis.NewClient(&redis.Options{
		Addr:            server.listener.Addr().String(),
		Protocol:        2,
		DisableIdentity: true,
	})
}

type databaseAssetFake struct {
	started chan struct{}
	release chan struct{}
}

func (f *databaseAssetFake) wait() {
	close(f.started)
	<-f.release
}

func (f *databaseAssetFake) StoreAsset(any, int64, int) (int, error) {
	f.wait()
	return 42, nil
}

func (f *databaseAssetFake) FetchAsset(int64, int) (*Message, error) {
	f.wait()
	return &Message{ID: 42}, nil
}

func TestDatabaseAssetOperationsDoNotHoldDatabaseLockDuringIO(t *testing.T) {
	operations := map[string]func(*Database) error{
		"store": func(db *Database) error {
			_, err := db.StoreAsset("asset")
			return err
		},
		"fetch": func(db *Database) error {
			_, err := db.FetchAsset(42)
			return err
		},
	}
	for name, operation := range operations {
		t.Run(name, func(t *testing.T) {
			fake := &databaseAssetFake{started: make(chan struct{}), release: make(chan struct{})}
			db := NewDatabase(1)
			db.assets.SetTransport(fake)
			db.data["goroku.forums"] = map[string]any{
				"channel_id": int64(99),
				"forums_cache": map[string]any{
					"goroku-userbot": map[string]any{"Assets": 7},
				},
			}

			operationDone := make(chan error, 1)
			go func() { operationDone <- operation(db) }()
			<-fake.started

			writerDone := make(chan struct{})
			go func() {
				db.mu.Lock()
				db.data["writer"] = map[string]any{"queued": true}
				db.mu.Unlock()
				close(writerDone)
			}()
			select {
			case <-writerDone:
			case <-time.After(time.Second):
				t.Fatal("queued writer could not acquire db.mu during asset I/O")
			}

			close(fake.release)
			select {
			case err := <-operationDone:
				if err != nil {
					t.Fatalf("asset operation failed: %v", err)
				}
			case <-time.After(time.Second):
				t.Fatal("asset operation deadlocked with queued writer")
			}
		})
	}
}

func TestDatabaseDeepCopyClonesNestedMapsAndSlices(t *testing.T) {
	db := &Database{}
	src := map[string]map[string]any{
		"owner": {
			"nested": map[string]any{"key": "original"},
			"slice":  []any{map[string]any{"item": "original"}},
		},
	}

	copy := db.deepCopy(src)
	copy["owner"]["nested"].(map[string]any)["key"] = "changed"
	copy["owner"]["slice"].([]any)[0].(map[string]any)["item"] = "changed"

	if got := src["owner"]["nested"].(map[string]any)["key"]; got != "original" {
		t.Fatalf("deepCopy shared nested map with source, got %v", got)
	}
	if got := src["owner"]["slice"].([]any)[0].(map[string]any)["item"]; got != "original" {
		t.Fatalf("deepCopy shared nested slice value with source, got %v", got)
	}
}

type databaseCopyItem struct {
	Count int64
	Tags  []string
}

type databaseUnexportedMap struct {
	Visible string
	hidden  map[string][]int
}

type databaseUnexportedSlice struct {
	Visible string
	hidden  []int
}

type databaseUnexportedPointer struct {
	Visible string
	hidden  *int
}

type databaseUnexportedInterface struct {
	Visible string
	hidden  any
}

type databaseNestedUnexportedMutable struct {
	Value databaseUnexportedMap
}

func TestDatabaseSetAndGetDetachTypedMutableValues(t *testing.T) {
	tempDir := t.TempDir()
	originalBaseDir := BaseDir
	BaseDir = tempDir
	defer func() { BaseDir = originalBaseDir }()

	db := NewDatabase(9001)
	if err := db.Init(""); err != nil {
		t.Fatalf("Init failed: %v", err)
	}
	item := &databaseCopyItem{Count: 7, Tags: []string{"original"}}
	input := map[string][]*databaseCopyItem{"items": {item}}
	if err := db.Set("owner", "typed", input); err != nil {
		t.Fatal("Set failed")
	}

	item.Count = 8
	item.Tags[0] = "caller-mutated"
	input["items"] = append(input["items"], &databaseCopyItem{Count: 9})

	got, err := db.Get("owner", "typed", nil)
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	typed, ok := got.(map[string][]*databaseCopyItem)
	if !ok {
		t.Fatalf("Get type = %T, want map[string][]*databaseCopyItem", got)
	}
	if len(typed["items"]) != 1 || typed["items"][0].Count != 7 || typed["items"][0].Tags[0] != "original" {
		t.Fatalf("Set retained caller aliases: %#v", typed)
	}

	typed["items"][0].Count = 10
	typed["items"][0].Tags[0] = "get-mutated"
	typed["items"] = nil
	gotAgain, _ := db.Get("owner", "typed", nil)
	want := map[string][]*databaseCopyItem{"items": {{Count: 7, Tags: []string{"original"}}}}
	if !reflect.DeepEqual(gotAgain, want) {
		t.Fatalf("Get returned live data: got %#v, want %#v", gotAgain, want)
	}
}

func TestDatabaseResetAndUpdateDetachInputs(t *testing.T) {
	tempDir := t.TempDir()
	originalBaseDir := BaseDir
	BaseDir = tempDir
	defer func() { BaseDir = originalBaseDir }()

	db := NewDatabase(9002)
	if err := db.Init(""); err != nil {
		t.Fatalf("Init failed: %v", err)
	}
	resetItem := &databaseCopyItem{Count: 1, Tags: []string{"reset"}}
	reset := map[string]map[string]any{"reset": {"item": resetItem}}
	if err := db.Reset(reset); err != nil {
		t.Fatal("Reset failed")
	}
	resetItem.Count = 2
	resetItem.Tags[0] = "changed"
	reset["reset"]["item"] = nil
	gotReset, _ := db.Get("reset", "item", nil)
	if !reflect.DeepEqual(gotReset, &databaseCopyItem{Count: 1, Tags: []string{"reset"}}) {
		t.Fatalf("Reset retained caller aliases: %#v", gotReset)
	}

	updateItem := &databaseCopyItem{Count: 3, Tags: []string{"update"}}
	update := map[string]map[string]any{"updated": {"item": updateItem}}
	if err := db.Update(update); err != nil {
		t.Fatal("Update failed")
	}
	updateItem.Count = 4
	updateItem.Tags[0] = "changed"
	update["updated"]["item"] = nil
	gotUpdate, _ := db.Get("updated", "item", nil)
	if !reflect.DeepEqual(gotUpdate, &databaseCopyItem{Count: 3, Tags: []string{"update"}}) {
		t.Fatalf("Update retained caller aliases: %#v", gotUpdate)
	}
}

func TestDatabaseRejectsUnexportedMutableAliasesWithoutChangingStateOrFile(t *testing.T) {
	db := NewDatabase(9006)
	db.dbFile = filepath.Join(t.TempDir(), "database.json")
	db.initialized = true
	if err := db.Set("owner", "key", "unchanged"); err != nil {
		t.Fatal(err)
	}
	beforeMemory := db.Dump()
	beforeFile, err := os.ReadFile(db.dbFile)
	if err != nil {
		t.Fatal(err)
	}
	n := 1

	values := map[string]any{
		"map":       databaseUnexportedMap{Visible: "accepted-by-json", hidden: map[string][]int{"n": {1}}},
		"slice":     databaseUnexportedSlice{Visible: "accepted-by-json", hidden: []int{1}},
		"pointer":   databaseUnexportedPointer{Visible: "accepted-by-json", hidden: &n},
		"interface": databaseUnexportedInterface{Visible: "accepted-by-json", hidden: map[string]int{"n": 1}},
		"nested":    databaseNestedUnexportedMutable{Value: databaseUnexportedMap{Visible: "accepted-by-json", hidden: map[string][]int{"n": {1}}}},
	}
	for name, value := range values {
		t.Run("set-"+name, func(t *testing.T) {
			if err := db.Set("owner", "key", value); !errors.Is(err, ErrDatabaseInvalidValue) {
				t.Fatalf("Set error = %v, want ErrDatabaseInvalidValue", err)
			}
			assertDatabaseStateAndFile(t, db, beforeMemory, beforeFile)
		})
	}

	bad := values["nested"]
	if err := db.Reset(map[string]map[string]any{"replacement": {"bad": bad}}); !errors.Is(err, ErrDatabaseInvalidValue) {
		t.Fatalf("Reset error = %v, want ErrDatabaseInvalidValue", err)
	}
	assertDatabaseStateAndFile(t, db, beforeMemory, beforeFile)
	if err := db.Update(map[string]map[string]any{"owner": {"valid": "must-not-commit"}, "other": {"bad": bad}}); !errors.Is(err, ErrDatabaseInvalidValue) {
		t.Fatalf("Update error = %v, want ErrDatabaseInvalidValue", err)
	}
	assertDatabaseStateAndFile(t, db, beforeMemory, beforeFile)
}

type databaseExportedPointerValue struct {
	Child *databaseCopyItem
}

func TestDatabaseExportedNestedPointersRemainTypedAndDetachedAcrossReads(t *testing.T) {
	db := NewDatabase(9007)
	db.dbFile = filepath.Join(t.TempDir(), "database.json")
	db.initialized = true
	input := databaseExportedPointerValue{Child: &databaseCopyItem{Count: 1, Tags: []string{"original"}}}
	if err := db.Set("owner", "key", input); err != nil {
		t.Fatal(err)
	}
	input.Child.Count = 2
	input.Child.Tags[0] = "caller"

	got, err := db.Get("owner", "key", nil)
	if err != nil {
		t.Fatal(err)
	}
	typed, ok := got.(databaseExportedPointerValue)
	if !ok || typed.Child.Count != 1 || typed.Child.Tags[0] != "original" {
		t.Fatalf("Get value = %#v", got)
	}
	typed.Child.Count = 3
	typed.Child.Tags[0] = "get"
	dump := db.Dump()
	dump["owner"]["key"].(databaseExportedPointerValue).Child.Tags[0] = "dump"
	all := db.GetAll()
	all["owner"]["key"].(databaseExportedPointerValue).Child.Count = 4

	again, _ := db.Get("owner", "key", nil)
	want := databaseExportedPointerValue{Child: &databaseCopyItem{Count: 1, Tags: []string{"original"}}}
	if !reflect.DeepEqual(again, want) {
		t.Fatalf("read boundary retained alias: got %#v, want %#v", again, want)
	}
}

func TestDatabaseUpdateFailureIsAtomic(t *testing.T) {
	tempDir := t.TempDir()
	originalBaseDir := BaseDir
	BaseDir = tempDir
	defer func() { BaseDir = originalBaseDir }()

	db := NewDatabase(9003)
	if err := db.Init(""); err != nil {
		t.Fatalf("Init failed: %v", err)
	}
	if err := db.Set("owner", "existing", "unchanged"); err != nil {
		t.Fatal("initial Set failed")
	}
	before := db.Dump()

	invalid := map[string]map[string]any{
		"owner": {"valid": "must-not-commit"},
		"other": {"invalid": make(chan int)},
	}
	if err := db.Update(invalid); err == nil {
		t.Fatal("Update accepted an invalid value")
	}
	if got := db.Dump(); !reflect.DeepEqual(got, before) {
		t.Fatalf("invalid batch partially committed: got %#v, want %#v", got, before)
	}

	protected := map[string]map[string]any{
		"owner":                {"valid": "must-not-commit"},
		"GorokuPluginSecurity": {"blocked": true},
	}
	if err := db.Update(protected); err == nil {
		t.Fatal("Update accepted a protected-owner write")
	}
	if got := db.Dump(); !reflect.DeepEqual(got, before) {
		t.Fatalf("protected batch partially committed: got %#v, want %#v", got, before)
	}
}

func TestDatabaseSnapshotsRevisionsAndRollbackAreIsolated(t *testing.T) {
	tempDir := t.TempDir()
	originalBaseDir := BaseDir
	BaseDir = tempDir
	defer func() { BaseDir = originalBaseDir }()

	db := NewDatabase(9004)
	if err := db.Init(""); err != nil {
		t.Fatalf("Init failed: %v", err)
	}
	if err := db.Set("owner", "value", map[string][]int{"numbers": {1, 2}}); err != nil {
		t.Fatal("Set failed")
	}

	dump := db.Dump()
	all := db.GetAll()
	dump["owner"]["value"].(map[string][]int)["numbers"][0] = 10
	all["owner"]["value"].(map[string][]int)["numbers"][1] = 20
	got, _ := db.Get("owner", "value", nil)
	if !reflect.DeepEqual(got, map[string][]int{"numbers": {1, 2}}) {
		t.Fatalf("snapshot mutation reached live data: %#v", got)
	}

	db.mu.Lock()
	db.revisions = []map[string]map[string]any{db.deepCopy(db.data)}
	revision := db.revisions[0]
	db.mu.Unlock()
	if err := db.Rollback(); err != nil {
		t.Fatal("Rollback failed")
	}
	revision["owner"]["value"].(map[string][]int)["numbers"][0] = 99
	got, _ = db.Get("owner", "value", nil)
	if !reflect.DeepEqual(got, map[string][]int{"numbers": {1, 2}}) {
		t.Fatalf("rollback retained revision aliases: %#v", got)
	}
}

func TestDatabaseCallerMutationDoesNotRaceSaveOrRedisSnapshot(t *testing.T) {
	tempDir := t.TempDir()
	originalBaseDir := BaseDir
	BaseDir = tempDir
	defer func() { BaseDir = originalBaseDir }()

	server := newDatabaseRedisServer(t, false)
	client := newDatabaseRedisClient(server)
	defer client.Close()
	db := NewDatabase(9005)
	if err := db.Init(""); err != nil {
		t.Fatalf("Init failed: %v", err)
	}
	db.mu.Lock()
	db.redisClient = client
	db.lastRedisSave = time.Now().Unix()
	db.mu.Unlock()

	callerValue := map[string]any{"numbers": []int{1, 2, 3}, "nested": map[string]string{"key": "original"}}
	if err := db.Set("owner", "value", callerValue); err != nil {
		t.Fatal("Set failed")
	}
	stop := make(chan struct{})
	mutated := make(chan struct{})
	go func() {
		defer close(mutated)
		numbers := callerValue["numbers"].([]int)
		nested := callerValue["nested"].(map[string]string)
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
				numbers[0] = i
				nested["key"] = strconv.Itoa(i)
			}
		}
	}()
	for range 20 {
		if err := db.SaveContext(context.Background()); err != nil {
			t.Fatal("Save failed")
		}
		if err := db.flushRedis(context.Background()); err != nil {
			t.Fatalf("flushRedis failed: %v", err)
		}
	}
	close(stop)
	<-mutated

	got, _ := db.Get("owner", "value", nil)
	want := map[string]any{"numbers": []int{1, 2, 3}, "nested": map[string]string{"key": "original"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("caller mutation reached live data: got %#v, want %#v", got, want)
	}
	var redisValue map[string]map[string]any
	if err := json.Unmarshal(server.lastStoredValue(), &redisValue); err != nil {
		t.Fatalf("Redis snapshot is invalid: %v", err)
	}
	if got := redisValue["owner"]["value"].(map[string]any)["nested"].(map[string]any)["key"]; got != "original" {
		t.Fatalf("Redis snapshot observed caller mutation: %v", got)
	}
}

func TestDatabaseCollectionGettersReturnCopiesAndEmptyDefaults(t *testing.T) {
	db := NewDatabase(1111)
	db.data["owner"] = map[string]any{
		"strings":    []string{"a"},
		"ids":        []int64{1},
		"string_map": map[string]string{"k": "v"},
		"any_map":    map[string]any{"k": "v", "nested": map[string]any{"n": "v"}},
		"slice_map":  map[string][]string{"k": {"v"}},
		"int_map":    map[string]int{"k": 1},
	}

	strings := db.GetStringSlice("owner", "strings", nil)
	strings[0] = "changed"
	if got := db.data["owner"]["strings"].([]string)[0]; got != "a" {
		t.Fatalf("GetStringSlice returned original slice, got %q", got)
	}

	ids := db.GetInt64Slice("owner", "ids", nil)
	ids[0] = 2
	if got := db.data["owner"]["ids"].([]int64)[0]; got != 1 {
		t.Fatalf("GetInt64Slice returned original slice, got %d", got)
	}

	stringMap := db.GetStringMap("owner", "string_map", nil)
	stringMap["k"] = "changed"
	if got := db.data["owner"]["string_map"].(map[string]string)["k"]; got != "v" {
		t.Fatalf("GetStringMap returned original map, got %q", got)
	}

	anyMap := db.GetAnyMap("owner", "any_map", nil)
	anyMap["k"] = "changed"
	anyMap["nested"].(map[string]any)["n"] = "changed"
	if got := db.data["owner"]["any_map"].(map[string]any)["k"]; got != "v" {
		t.Fatalf("GetAnyMap returned original map, got %v", got)
	}
	if got := db.data["owner"]["any_map"].(map[string]any)["nested"].(map[string]any)["n"]; got != "v" {
		t.Fatalf("GetAnyMap returned original nested map, got %v", got)
	}

	sliceMap := db.GetStringMapStringSlice("owner", "slice_map", nil)
	sliceMap["k"][0] = "changed"
	if got := db.data["owner"]["slice_map"].(map[string][]string)["k"][0]; got != "v" {
		t.Fatalf("GetStringMapStringSlice returned original nested slice, got %q", got)
	}

	intMap := db.GetStringMapInt("owner", "int_map", nil)
	intMap["k"] = 2
	if got := db.data["owner"]["int_map"].(map[string]int)["k"]; got != 1 {
		t.Fatalf("GetStringMapInt returned original map, got %d", got)
	}

	if got := db.GetStringMap("owner", "missing_string_map", nil); got == nil {
		t.Fatal("GetStringMap returned nil for nil default")
	}
	if got := db.GetAnyMap("owner", "missing_any_map", nil); got == nil {
		t.Fatal("GetAnyMap returned nil for nil default")
	}
	if got := db.GetStringSlice("owner", "missing_strings", nil); got == nil {
		t.Fatal("GetStringSlice returned nil for nil default")
	}
}

func TestDatabaseCRUDOperations(t *testing.T) {
	tempDir := t.TempDir()
	originalBaseDir := BaseDir
	BaseDir = tempDir
	defer func() { BaseDir = originalBaseDir }()

	db := NewDatabase(12345)
	err := db.Init("")
	if err != nil {
		t.Fatalf("Failed to initialize database: %v", err)
	}

	// Test Set and Get
	if err := db.Set("test_module", "key1", "value1"); err != nil {
		t.Fatal("Set failed")
	}

	val, _ := db.Get("test_module", "key1", "default")
	if val != "value1" {
		t.Fatalf("Expected 'value1', got '%v'", val)
	}

	// Test case-insensitivity of module name
	valFold, _ := db.Get("TEST_module", "key1", "default")
	if valFold != "value1" {
		t.Fatalf("Expected 'value1' with case-insensitive check, got '%v'", valFold)
	}

	// Test Dump
	dump := db.Dump()
	if dump["test_module"]["key1"] != "value1" {
		t.Fatalf("Dump does not contain correct value: %v", dump)
	}

	// Test Delete
	if err := db.Delete("test_module", "key1"); err != nil {
		t.Fatal("Delete failed")
	}

	valDeleted, _ := db.Get("test_module", "key1", "default")
	if valDeleted != "default" {
		t.Fatalf("Expected default value after delete, got '%v'", valDeleted)
	}
}

func TestDatabaseRevisionsAndRollback(t *testing.T) {
	tempDir := t.TempDir()
	originalBaseDir := BaseDir
	BaseDir = tempDir
	defer func() { BaseDir = originalBaseDir }()

	db := NewDatabase(54321)
	err := db.Init("")
	if err != nil {
		t.Fatalf("Failed to initialize database: %v", err)
	}

	// We force a rollback check. Initially no revisions
	if err := db.Rollback(); err == nil {
		t.Fatal("Rollback should fail when no revisions exist")
	}

	if err := db.Set("mod", "k1", "initial"); err != nil {
		t.Fatal(err)
	}
	if err := db.Set("mod", "k1", "second"); err != nil {
		t.Fatal(err)
	}

	if val, _ := db.Get("mod", "k1", ""); val != "second" {
		t.Fatalf("Expected second, got %v", val)
	}

	if err := db.Rollback(); err != nil {
		t.Fatal("Rollback failed")
	}

	if val, _ := db.Get("mod", "k1", ""); val != "initial" {
		t.Fatalf("Expected initial after rollback, got %v", val)
	}
}

func TestDatabaseLegacyPrefixConversion(t *testing.T) {
	tempDir := t.TempDir()
	originalBaseDir := BaseDir
	BaseDir = tempDir
	defer func() { BaseDir = originalBaseDir }()

	tgID := int64(98765)
	dbPath := filepath.Join(tempDir, fmt.Sprintf("config-%d.json", tgID))

	// Write legacy data manually
	legacyData := map[string]any{
		"hikka.module": map[string]any{
			"foo": "bar",
		},
		"legacy.test": map[string]any{
			"abc": 123,
		},
		"heroku.other": map[string]any{
			"xyz": true,
		},
	}
	bytes, err := json.Marshal(legacyData)
	if err != nil {
		t.Fatalf("Failed to marshal legacy data: %v", err)
	}
	err = os.WriteFile(dbPath, bytes, 0600)
	if err != nil {
		t.Fatalf("Failed to write legacy file: %v", err)
	}

	db := NewDatabase(tgID)
	err = db.Init("")
	if err != nil {
		t.Fatalf("Failed to initialize database: %v", err)
	}

	// Verify prefix conversion took place
	if val, _ := db.Get("goroku.module", "foo", nil); val != "bar" {
		t.Fatalf("Expected 'bar' from goroku.module, got '%v'", val)
	}
	if val, _ := db.Get("goroku.test", "abc", nil); val != float64(123) {
		t.Fatalf("Expected 123 from goroku.test, got '%v'", val)
	}
	if val, _ := db.Get("goroku.other", "xyz", nil); val != true {
		t.Fatalf("Expected true from goroku.other, got '%v'", val)
	}
}

func TestDatabaseAutofix(t *testing.T) {
	db := NewDatabase(1111)
	db.data["some_module"] = nil // empty module keys should be removed

	processDBAutofix(db.data)

	if _, ok := db.data["some_module"]; ok {
		t.Fatal("Expected nil module key to be removed by autofix")
	}
}

func TestDatabaseNormalizeOwnerDirect(t *testing.T) {
	db := NewDatabase(1111)
	db.data["TestOwner"] = map[string]any{"key": "val"}
	// exact match
	if got := db.normalizeOwner("TestOwner"); got != "TestOwner" {
		t.Errorf("normalizeOwner exact match failed: got %q, want %q", got, "TestOwner")
	}
	// case-insensitive match from db.data
	if got := db.normalizeOwner("testowner"); got != "TestOwner" {
		t.Errorf("normalizeOwner case-insensitive failed: got %q, want %q", got, "TestOwner")
	}
	// fallback to original
	if got := db.normalizeOwner("NonExistent"); got != "NonExistent" {
		t.Errorf("normalizeOwner fallback failed: got %q, want %q", got, "NonExistent")
	}
}

func TestDatabaseDeleteOwner(t *testing.T) {
	tempDir := t.TempDir()
	originalBaseDir := BaseDir
	BaseDir = tempDir
	defer func() { BaseDir = originalBaseDir }()

	db := NewDatabase(1111)
	_ = db.Init("")
	db.Set("mod1", "k", "v")
	db.Set("mod2", "k", "v")

	if err := db.DeleteOwner("mod1"); err != nil {
		t.Fatal("DeleteOwner failed")
	}

	if val, _ := db.Get("mod1", "k", nil); val != nil {
		t.Fatalf("expected mod1 to be deleted, got %v", val)
	}
	if val, _ := db.Get("mod2", "k", nil); val == nil {
		t.Fatal("expected mod2 to remain")
	}
}

func TestDatabaseReset(t *testing.T) {
	tempDir := t.TempDir()
	originalBaseDir := BaseDir
	BaseDir = tempDir
	defer func() { BaseDir = originalBaseDir }()

	db := NewDatabase(1111)
	_ = db.Init("")
	db.Set("mod1", "k", "v")

	newData := map[string]map[string]any{
		"new_mod": {"nk": "nv"},
	}

	if err := db.Reset(newData); err != nil {
		t.Fatal("Reset failed")
	}

	if val, _ := db.Get("mod1", "k", nil); val != nil {
		t.Fatal("expected old data to be cleared")
	}
	if val, _ := db.Get("new_mod", "nk", nil); val != "nv" {
		t.Fatalf("expected new_mod to exist, got %v", val)
	}
}

func TestDatabaseGetAll(t *testing.T) {
	db := NewDatabase(1111)
	db.data = map[string]map[string]any{
		"m": {"k": "v"},
	}
	all := db.GetAll()
	all["m"]["k"] = "changed"

	if db.data["m"]["k"] != "v" {
		t.Fatal("GetAll did not perform a deep copy")
	}
}

func TestDatabaseRedisSaveLogic(t *testing.T) {
	tempDir := t.TempDir()
	originalBaseDir := BaseDir
	BaseDir = tempDir
	defer func() { BaseDir = originalBaseDir }()

	db := NewDatabase(1111)
	_ = db.Init("")
	db.redisClient = redis.NewClient(&redis.Options{Addr: "localhost:12345"}) // non-existent redis
	db.Set("m", "k", "v")

	// Since lastRedisSave is 0, now - lastRedisSave >= 5.
	// It will attempt to write to Redis, fail, and fall back to local file.
	// We check that saveInner returned true because file save succeeded.
	db.lastRedisSave = time.Now().Unix()
	// Modify key to trigger save
	db.Set("m", "k2", "v2")
	// Since lastRedisSave was just set to now, now - lastRedisSave < 5, so it should set redisDirty = true
	if !db.redisDirty {
		t.Fatal("expected redisDirty to be true when saved within 5 seconds")
	}
}

func TestDatabaseSuccessfulRedisSaveAlsoUpdatesAtomicLocalFallback(t *testing.T) {
	tempDir := t.TempDir()
	originalBaseDir := BaseDir
	BaseDir = tempDir
	defer func() { BaseDir = originalBaseDir }()

	server := newDatabaseRedisServer(t, false)
	db := NewDatabase(2222)
	if err := db.Init(""); err != nil {
		t.Fatalf("Init failed: %v", err)
	}
	db.redisClient = newDatabaseRedisClient(server)
	defer db.redisClient.Close()

	if err := db.Set("owner", "key", "current"); err != nil {
		t.Fatal("Set failed")
	}
	content, err := os.ReadFile(db.dbFile)
	if err != nil {
		t.Fatalf("read local fallback: %v", err)
	}
	var stored map[string]map[string]any
	if err := json.Unmarshal(content, &stored); err != nil {
		t.Fatalf("local fallback is invalid JSON: %v", err)
	}
	if got := stored["owner"]["key"]; got != "current" {
		t.Fatalf("local fallback is stale: got %v", got)
	}
	info, err := os.Stat(db.dbFile)
	if err != nil {
		t.Fatalf("stat local fallback: %v", err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("local fallback mode = %o, want 600", info.Mode().Perm())
	}
	temps, err := filepath.Glob(filepath.Join(tempDir, ".goroku-db-*"))
	if err != nil {
		t.Fatalf("glob temporary files: %v", err)
	}
	if len(temps) != 0 {
		t.Fatalf("atomic save left temporary files: %v", temps)
	}
}

func TestDatabaseRedisFlushPreservesDirtyOnConcurrentMutation(t *testing.T) {
	server := newDatabaseRedisServer(t, true)
	client := newDatabaseRedisClient(server)
	defer client.Close()
	db := NewDatabase(3333)
	db.redisClient = client
	db.data["owner"] = map[string]any{"key": "before"}
	db.generation = 1
	db.redisDirty = true

	flushDone := make(chan error, 1)
	go func() { flushDone <- db.flushRedis(context.Background()) }()
	select {
	case <-server.setStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("Redis SET did not start")
	}

	mutationDone := make(chan struct{})
	go func() {
		db.mu.Lock()
		db.data["owner"]["key"] = "after"
		db.generation++
		db.redisDirty = true
		db.mu.Unlock()
		close(mutationDone)
	}()
	select {
	case <-mutationDone:
	case <-time.After(time.Second):
		t.Fatal("Redis I/O held the database mutex")
	}

	close(server.releaseSet)
	if err := <-flushDone; err != nil {
		t.Fatalf("flushRedis failed: %v", err)
	}
	db.mu.RLock()
	dirty := db.redisDirty
	db.mu.RUnlock()
	if !dirty {
		t.Fatal("flush cleared dirty state for a newer generation")
	}
}

func TestDatabaseInitRejectsInvalidRedisURL(t *testing.T) {
	db := NewDatabase(4444)
	if err := db.Init("://not-a-redis-url"); err == nil {
		t.Fatal("Init accepted an invalid Redis URL")
	}
}

func TestDatabaseCloseStopsFlushLoopAndClosesRedis(t *testing.T) {
	tempDir := t.TempDir()
	originalBaseDir := BaseDir
	BaseDir = tempDir
	defer func() { BaseDir = originalBaseDir }()

	server := newDatabaseRedisServer(t, false)
	db := NewDatabase(5555)
	if err := db.Init("redis://" + server.listener.Addr().String() + "/0"); err != nil {
		t.Fatalf("Init failed: %v", err)
	}
	client := db.redisClient
	done := db.flushDone
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := db.Close(ctx); err != nil {
		t.Fatalf("Close failed: %v", err)
	}
	select {
	case <-done:
	default:
		t.Fatal("flush loop is still running after Close")
	}
	if err := client.Ping(context.Background()).Err(); err == nil {
		t.Fatal("Redis client remained usable after Close")
	}
	if err := db.Close(ctx); err != nil {
		t.Fatalf("second Close failed: %v", err)
	}
}

func TestDatabaseInitPrefersLocalAndReplacesStaleRedis(t *testing.T) {
	tempDir := t.TempDir()
	originalBaseDir := BaseDir
	BaseDir = tempDir
	defer func() { BaseDir = originalBaseDir }()

	tgID := int64(6666)
	local := []byte(`{"owner":{"key":"newer-local"}}`)
	if err := os.WriteFile(filepath.Join(tempDir, fmt.Sprintf("config-%d.json", tgID)), local, 0600); err != nil {
		t.Fatalf("write local database: %v", err)
	}
	server := newDatabaseRedisServer(t, false)
	server.setStoredValue([]byte(`{"owner":{"key":"stale-redis"}}`))

	db := NewDatabase(tgID)
	if err := db.Init("redis://" + server.listener.Addr().String() + "/0"); err != nil {
		t.Fatalf("Init failed: %v", err)
	}
	defer db.Close(context.Background())
	if got := db.GetString("owner", "key", ""); got != "newer-local" {
		t.Fatalf("startup selected %q, want newer local state", got)
	}
	var synced map[string]map[string]any
	if err := json.Unmarshal(server.lastStoredValue(), &synced); err != nil {
		t.Fatalf("Redis did not receive valid local state: %v", err)
	}
	if got := synced["owner"]["key"]; got != "newer-local" {
		t.Fatalf("Redis retained stale state: got %v", got)
	}
}

func TestDatabaseCloseFinalFlushesDirtyRedis(t *testing.T) {
	tempDir := t.TempDir()
	originalBaseDir := BaseDir
	BaseDir = tempDir
	defer func() { BaseDir = originalBaseDir }()

	server := newDatabaseRedisServer(t, false)
	db := NewDatabase(7777)
	if err := db.Init("redis://" + server.listener.Addr().String() + "/0"); err != nil {
		t.Fatalf("Init failed: %v", err)
	}
	db.mu.Lock()
	db.lastRedisSave = time.Now().Unix()
	db.mu.Unlock()
	if err := db.Set("owner", "key", "final"); err != nil {
		t.Fatal("Set failed")
	}
	if value := server.lastStoredValue(); value != nil {
		t.Fatalf("Redis was flushed before Close: %s", value)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := db.Close(ctx); err != nil {
		t.Fatalf("Close failed: %v", err)
	}
	finalRedis := server.lastStoredValue()
	var flushed map[string]map[string]any
	if err := json.Unmarshal(finalRedis, &flushed); err != nil {
		t.Fatalf("final flush is invalid: %v", err)
	}
	if got := flushed["owner"]["key"]; got != "final" {
		t.Fatalf("final flush stored %v", got)
	}
	if err := db.Set("owner", "key", "after-close"); err == nil {
		t.Fatal("Set succeeded after final flush")
	}
	if got := server.lastStoredValue(); !reflect.DeepEqual(got, finalRedis) {
		t.Fatalf("Redis changed after final flush: %s", got)
	}
}

func TestDatabaseCloseFinalFlushHonorsContextTimeout(t *testing.T) {
	tempDir := t.TempDir()
	originalBaseDir := BaseDir
	BaseDir = tempDir
	defer func() { BaseDir = originalBaseDir }()

	server := newDatabaseRedisServer(t, true)
	db := NewDatabase(8888)
	if err := db.Init("redis://" + server.listener.Addr().String() + "/0"); err != nil {
		t.Fatalf("Init failed: %v", err)
	}
	db.mu.Lock()
	db.lastRedisSave = time.Now().Unix()
	db.mu.Unlock()
	if err := db.Set("owner", "key", "pending"); err != nil {
		t.Fatal("Set failed")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	closeResult := make(chan error, 1)
	go func() { closeResult <- db.Close(ctx) }()
	select {
	case <-server.setStarted:
	case <-time.After(time.Second):
		t.Fatal("Close did not attempt final Redis flush")
	}
	select {
	case err := <-closeResult:
		if err != context.DeadlineExceeded {
			t.Fatalf("Close error = %v, want context deadline exceeded", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Close ignored context timeout")
	}
	close(server.releaseSet)
}

func TestDatabaseCloseSerializesWithSaveAndQueuedSet(t *testing.T) {
	tempDir := t.TempDir()
	originalBaseDir := BaseDir
	BaseDir = tempDir
	defer func() { BaseDir = originalBaseDir }()

	db := NewDatabase(9991)
	if err := db.Init(""); err != nil {
		t.Fatalf("Init failed: %v", err)
	}
	if err := db.Set("owner", "key", "before-close"); err != nil {
		t.Fatal("initial Set failed")
	}

	// Hold the persistence gate so Save, Set, and Close are all queued before
	// checking that Close's terminal transition rejects both writes.
	db.persistMu.Lock()

	saveDone := make(chan error, 1)
	go func() { saveDone <- db.SaveContext(context.Background()) }()
	setDone := make(chan error, 1)
	go func() { setDone <- db.Set("owner", "late", "must-not-commit") }()
	closeDone := make(chan error, 1)
	go func() { closeDone <- db.Close(context.Background()) }()

	deadline := time.Now().Add(time.Second)
	for {
		db.mu.RLock()
		closing := db.closing
		db.mu.RUnlock()
		if closing {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("Close did not enter closing state")
		}
		runtime.Gosched()
	}
	postStartDone := make(chan error, 1)
	go func() { postStartDone <- db.Set("owner", "post-start", true) }()
	db.persistMu.Unlock()
	if err := <-saveDone; !errors.Is(err, ErrDatabaseClosed) {
		t.Fatalf("Save queued when Close started error = %v", err)
	}
	if err := <-setDone; !errors.Is(err, ErrDatabaseClosed) {
		t.Fatalf("Set queued behind Save error = %v", err)
	}
	if err := <-postStartDone; !errors.Is(err, ErrDatabaseClosed) {
		t.Fatalf("Set beginning after Close started error = %v", err)
	}
	if err := <-closeDone; err != nil {
		t.Fatalf("Close failed: %v", err)
	}
	if got, _ := db.Get("owner", "late", nil); got != nil {
		t.Fatalf("queued Set mutated closed database: %v", got)
	}
	if got, _ := db.Get("owner", "post-start", nil); got != nil {
		t.Fatalf("post-start Set mutated closed database: %v", got)
	}
	if got := db.Dump(); !reflect.DeepEqual(got, map[string]map[string]any{"owner": {"key": "before-close"}}) {
		t.Fatalf("mutation occurred during Close: %#v", got)
	}
}

func TestDatabaseWritesFailAfterClose(t *testing.T) {
	tempDir := t.TempDir()
	originalBaseDir := BaseDir
	BaseDir = tempDir
	defer func() { BaseDir = originalBaseDir }()

	db := NewDatabase(9992)
	if err := db.Init(""); err != nil {
		t.Fatalf("Init failed: %v", err)
	}
	if err := db.Set("owner", "key", "original"); err != nil {
		t.Fatal("initial Set failed")
	}
	before := db.Dump()
	if err := db.Close(context.Background()); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	writes := map[string]func() error{
		"save":         db.Save,
		"set":          func() error { return db.Set("owner", "key", "changed") },
		"delete":       func() error { return db.Delete("owner", "key") },
		"reset":        func() error { return db.Reset(map[string]map[string]any{"new": {"key": true}}) },
		"update":       func() error { return db.Update(map[string]map[string]any{"owner": {"new": true}}) },
		"delete-owner": func() error { return db.DeleteOwner("owner") },
		"rollback":     db.Rollback,
	}
	for name, write := range writes {
		if err := write(); !errors.Is(err, ErrDatabaseClosed) {
			t.Errorf("%s error after Close = %v", name, err)
		}
	}
	if got := db.Dump(); !reflect.DeepEqual(got, before) {
		t.Fatalf("post-Close write mutated data: got %#v, want %#v", got, before)
	}
}

func TestDatabaseErrorSentinelsAndContext(t *testing.T) {
	uninitialized := NewDatabase(1)
	if err := uninitialized.Set("owner", "key", true); !errors.Is(err, ErrDatabaseNotInitialized) {
		t.Fatalf("uninitialized Set error = %v", err)
	}

	tempDir := t.TempDir()
	db := NewDatabase(2)
	db.dbFile = filepath.Join(tempDir, "database.json")
	db.initialized = true

	invalidErr := db.Set("owner", "key", make(chan int))
	if !errors.Is(invalidErr, ErrDatabaseInvalidValue) {
		t.Fatalf("invalid Set error = %v", invalidErr)
	}
	var diagnostic *DatabaseError
	if !errors.As(invalidErr, &diagnostic) {
		t.Fatalf("invalid Set did not return DatabaseError: %T", invalidErr)
	}
	if diagnostic.Operation != "set" || diagnostic.Owner != "owner" || diagnostic.Key != "key" || diagnostic.Backend != "local" {
		t.Fatalf("unexpected diagnostic: %#v", diagnostic)
	}

	if err := db.Set("GorokuPluginSecurity", "key", true); !errors.Is(err, ErrDatabaseWriteProtected) {
		t.Fatalf("protected Set error = %v", err)
	}
	if err := db.Rollback(); !errors.Is(err, ErrDatabaseNoRevision) {
		t.Fatalf("empty Rollback error = %v", err)
	}

	injected := errors.New("injected local failure")
	db.writeLocal = func(string, []byte) error { return injected }
	persistErr := db.Set("owner", "key", true)
	if !errors.Is(persistErr, ErrDatabasePersistence) || !errors.Is(persistErr, injected) {
		t.Fatalf("persistence error = %v", persistErr)
	}

	db.writeLocal = writeFileAtomic
	if err := db.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := db.Set("owner", "key", true); !errors.Is(err, ErrDatabaseClosed) {
		t.Fatalf("closed Set error = %v", err)
	}
}

func TestDatabaseRejectedAndLocalPersistenceFailuresAreAtomic(t *testing.T) {
	db := NewDatabase(3)
	db.dbFile = filepath.Join(t.TempDir(), "database.json")
	db.initialized = true
	if err := db.Set("owner", "key", "old"); err != nil {
		t.Fatal(err)
	}
	beforeMemory := db.Dump()
	beforeFile, err := os.ReadFile(db.dbFile)
	if err != nil {
		t.Fatal(err)
	}

	for name, write := range map[string]func() error{
		"invalid":   func() error { return db.Set("owner", "key", make(chan int)) },
		"protected": func() error { return db.Set("GorokuPluginSecurity", "key", true) },
	} {
		t.Run(name, func(t *testing.T) {
			if err := write(); err == nil {
				t.Fatal("write unexpectedly succeeded")
			}
			assertDatabaseStateAndFile(t, db, beforeMemory, beforeFile)
		})
	}

	for _, stage := range []string{"create", "write", "sync", "rename"} {
		t.Run(stage, func(t *testing.T) {
			db.writeLocal = func(string, []byte) error { return fmt.Errorf("injected %s failure", stage) }
			if err := db.Set("owner", "key", stage); !errors.Is(err, ErrDatabasePersistence) {
				t.Fatalf("Set error = %v", err)
			}
			assertDatabaseStateAndFile(t, db, beforeMemory, beforeFile)
		})
	}
}

func assertDatabaseStateAndFile(t *testing.T, db *Database, wantMemory map[string]map[string]any, wantFile []byte) {
	t.Helper()
	if got := db.Dump(); !reflect.DeepEqual(got, wantMemory) {
		t.Fatalf("memory changed: got %#v, want %#v", got, wantMemory)
	}
	gotFile, err := os.ReadFile(db.dbFile)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(gotFile, wantFile) {
		t.Fatalf("file changed: got %s, want %s", gotFile, wantFile)
	}
}

func TestDatabaseSaveCancellationWhileWaiting(t *testing.T) {
	db := NewDatabase(4)
	db.dbFile = filepath.Join(t.TempDir(), "database.json")
	db.initialized = true
	db.persistMu.Lock()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- db.SaveContext(ctx) }()
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Save error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Save did not honor cancellation")
	}
	db.persistMu.Unlock()
}

func TestDatabaseRollbackIsDurable(t *testing.T) {
	db := NewDatabase(5)
	db.dbFile = filepath.Join(t.TempDir(), "database.json")
	db.initialized = true
	if err := db.Set("owner", "key", "first"); err != nil {
		t.Fatal(err)
	}
	if err := db.Set("owner", "key", "second"); err != nil {
		t.Fatal(err)
	}
	if err := db.Rollback(); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(db.dbFile)
	if err != nil {
		t.Fatal(err)
	}
	var stored map[string]map[string]any
	if err := json.Unmarshal(content, &stored); err != nil {
		t.Fatal(err)
	}
	if got := stored["owner"]["key"]; got != "first" {
		t.Fatalf("durable rollback value = %v", got)
	}
}

func TestDatabaseSaveCompatibilityInterfaces(t *testing.T) {
	db := NewDatabase(51)
	db.dbFile = filepath.Join(t.TempDir(), "database.json")
	db.initialized = true

	var saver databaseSaver = db
	if err := saver.Save(); err != nil {
		t.Fatalf("Save failed: %v", err)
	}
	var contextSaver databaseContextSaver = db
	if err := contextSaver.SaveContext(context.Background()); err != nil {
		t.Fatalf("SaveContext failed: %v", err)
	}
}

func TestDatabaseAtomicWriteCommitBoundary(t *testing.T) {
	stages := []string{"pre-rename", "post-rename-open", "post-rename-sync", "post-rename-close", "post-rename-cleanup"}
	for _, stage := range stages {
		t.Run(stage, func(t *testing.T) {
			db := NewDatabase(57)
			db.dbFile = filepath.Join(t.TempDir(), "database.json")
			db.initialized = true
			if err := db.Set("owner", "key", "old"); err != nil {
				t.Fatal(err)
			}
			beforeMemory := db.Dump()
			beforeFile, err := os.ReadFile(db.dbFile)
			if err != nil {
				t.Fatal(err)
			}
			beforeGeneration := db.generation
			beforeRevisions := len(db.revisions)

			ops := defaultAtomicFileOps
			calls := 0
			// stageLocalCandidate fsyncs the temp file only; commitLocalCandidate dir syncs:
			// 1 = pre-rename backup snapshot dir sync (when previous file exists)
			// 2 = post-rename primary dir sync
			// 3 = last-valid retention dir sync
			switch stage {
			case "pre-rename":
				ops.rename = func(string, string) error { return errors.New("injected rename failure") }
			case "post-rename-open":
				original := ops.openDir
				ops.openDir = func(path string) (*os.File, error) {
					calls++
					if calls == 2 {
						return nil, errors.New("injected post-rename open failure")
					}
					return original(path)
				}
			case "post-rename-sync":
				original := ops.syncDir
				ops.syncDir = func(file *os.File) error {
					calls++
					if calls == 2 {
						return errors.New("injected post-rename sync failure")
					}
					return original(file)
				}
			case "post-rename-close":
				original := ops.closeDir
				ops.closeDir = func(file *os.File) error {
					calls++
					err := original(file)
					if calls == 2 {
						return errors.Join(err, errors.New("injected post-rename close failure"))
					}
					return err
				}
			case "post-rename-cleanup":
				original := ops.syncDir
				ops.syncDir = func(file *os.File) error {
					calls++
					if calls == 3 {
						return errors.New("injected last-valid retention failure")
					}
					return original(file)
				}
			}

			db.writeLocal = func(path string, data []byte) error {
				return writeFileAtomicWithOps(path, data, ops)
			}
			err = db.Set("owner", "key", "new")
			restarted := NewDatabase(57)
			restarted.dbFile = db.dbFile
			restarted.data, restarted.initialized = restarted.readLocal(db.dbFile)
			restartValue, restartErr := restarted.Get("owner", "key", nil)
			if restartErr != nil {
				t.Fatal(restartErr)
			}

			if stage == "pre-rename" {
				if !errors.Is(err, ErrDatabasePersistence) || errors.Is(err, ErrDatabaseCommitUncertain) {
					t.Fatalf("pre-rename error = %v", err)
				}
				assertDatabaseStateAndFile(t, db, beforeMemory, beforeFile)
				if db.generation != beforeGeneration || len(db.revisions) != beforeRevisions {
					t.Fatalf("pre-rename state published: generation=%d revisions=%d", db.generation, len(db.revisions))
				}
				if restartValue != "old" {
					t.Fatalf("restart value = %v, want old", restartValue)
				}
				return
			}

			if err != nil {
				t.Fatalf("post-rename logical commit error = %v", err)
			}
			assertCommittedWarning(t, db.DurabilityWarning(), nil)
			if got, getErr := db.Get("owner", "key", nil); getErr != nil || got != "new" {
				t.Fatalf("memory value/error = %v/%v", got, getErr)
			}
			if restartValue != "new" {
				t.Fatalf("restart value = %v, want new", restartValue)
			}
			if db.generation != beforeGeneration+1 || len(db.revisions) != beforeRevisions+1 {
				t.Fatalf("post-rename publication: generation=%d revisions=%d", db.generation, len(db.revisions))
			}
		})
	}
}

func TestDatabaseGetLifecycleDiagnosticsAndActiveFallback(t *testing.T) {
	uninitialized := NewDatabase(58)
	if got, err := uninitialized.Get("owner", "key", "fallback"); got != "fallback" || !errors.Is(err, ErrDatabaseNotInitialized) {
		t.Fatalf("uninitialized Get = %v, %v", got, err)
	}
	if got := uninitialized.GetString("owner", "key", "fallback"); got != "fallback" {
		t.Fatalf("typed uninitialized Get = %q", got)
	}

	db := NewDatabase(59)
	db.dbFile = filepath.Join(t.TempDir(), "database.json")
	db.initialized = true
	if got, err := db.Get("owner", "missing", "fallback"); got != "fallback" || err != nil {
		t.Fatalf("active missing Get = %v, %v", got, err)
	}
	db.closing = true
	if got, err := db.Get("owner", "key", "fallback"); got != "fallback" || !errors.Is(err, ErrDatabaseClosed) {
		t.Fatalf("closing Get = %v, %v", got, err)
	}
	db.closing = false
	db.closed = true
	if got, err := db.Get("owner", "key", "fallback"); got != "fallback" || !errors.Is(err, ErrDatabaseClosed) {
		t.Fatalf("closed Get = %v, %v", got, err)
	}
}

func TestDatabaseRollbackConsumesRevisionAfterCommittedWarning(t *testing.T) {
	db := NewDatabase(60)
	db.dbFile = filepath.Join(t.TempDir(), "database.json")
	db.initialized = true
	if err := db.Set("owner", "key", "first"); err != nil {
		t.Fatal(err)
	}
	if err := db.Set("owner", "key", "second"); err != nil {
		t.Fatal(err)
	}
	beforeRevisions := len(db.revisions)

	ops := defaultAtomicFileOps
	calls := 0
	// See TestDatabaseAtomicWriteCommitBoundary: post-rename primary dir sync is call 2.
	const postRenameDirCall = 2
	original := ops.syncDir
	ops.syncDir = func(file *os.File) error {
		calls++
		if calls == postRenameDirCall {
			return errors.New("injected post-rename sync failure")
		}
		return original(file)
	}
	db.writeLocal = func(path string, data []byte) error {
		return writeFileAtomicWithOps(path, data, ops)
	}

	err := db.Rollback()
	if err != nil {
		t.Fatalf("Rollback logical commit error = %v", err)
	}
	assertCommittedWarning(t, db.DurabilityWarning(), nil)
	if len(db.revisions) != beforeRevisions-1 {
		t.Fatalf("committed Rollback retained consumed revision: %d", len(db.revisions))
	}
	if got := db.Dump()["owner"]["key"]; got != "first" {
		t.Fatalf("committed Rollback memory value = %v", got)
	}
	restarted := NewDatabase(60)
	restarted.dbFile = db.dbFile
	restarted.data, restarted.initialized = restarted.readLocal(db.dbFile)
	if got, getErr := restarted.Get("owner", "key", nil); getErr != nil || got != "first" {
		t.Fatalf("committed Rollback restart value/error = %v/%v", got, getErr)
	}
}

func TestDatabaseRetainsDurabilityWarningUntilDurableCurrentGeneration(t *testing.T) {
	db := NewDatabase(61)
	db.dbFile = filepath.Join(t.TempDir(), "database.json")
	db.initialized = true
	if err := db.Set("owner", "key", "old"); err != nil {
		t.Fatal(err)
	}

	cause := errors.New("injected post-rename warning")
	installPostRenameWarning(t, db, cause)
	if err := db.Set("owner", "key", "new"); err != nil {
		t.Fatalf("Set returned a post-rename warning: %v", err)
	}
	warning := db.DurabilityWarning()
	assertCommittedWarning(t, warning, cause)

	db.writeLocal = func(string, []byte) error { return errors.New("injected pre-rename failure") }
	if err := db.Save(); !errors.Is(err, ErrDatabasePersistence) {
		t.Fatalf("failed Save error = %v", err)
	}
	if got := db.DurabilityWarning(); got != warning {
		t.Fatalf("pre-rename failure changed retained warning: %v", got)
	}

	db.writeLocal = writeFileAtomic
	if err := db.Save(); err != nil {
		t.Fatalf("durable Save failed: %v", err)
	}
	if warning := db.DurabilityWarning(); warning != nil {
		t.Fatalf("durable Save retained warning: %v", warning)
	}
	if err := db.Close(context.Background()); err != nil {
		t.Fatalf("Close after durable Save = %v", err)
	}
}

func TestDatabaseOrdinaryWritesReturnLogicalSuccessAfterRename(t *testing.T) {
	writes := map[string]func(*Database) error{
		"set":    func(db *Database) error { return db.Set("owner", "key", "new") },
		"delete": func(db *Database) error { return db.Delete("owner", "key") },
		"reset": func(db *Database) error {
			return db.Reset(map[string]map[string]any{"replacement": {"key": true}})
		},
		"update": func(db *Database) error {
			return db.Update(map[string]map[string]any{"owner": {"key": "new"}})
		},
		"save": func(db *Database) error { return db.Save() },
	}
	for name, write := range writes {
		t.Run(name, func(t *testing.T) {
			db := NewDatabase(64)
			db.dbFile = filepath.Join(t.TempDir(), "database.json")
			db.initialized = true
			if err := db.Set("owner", "key", "old"); err != nil {
				t.Fatal(err)
			}
			cause := errors.New("injected " + name + " durability warning")
			installPostRenameWarning(t, db, cause)

			if err := write(db); err != nil {
				t.Fatalf("%s returned a post-rename warning: %v", name, err)
			}
			assertCommittedWarning(t, db.DurabilityWarning(), cause)
		})
	}
}

func TestDatabaseSaveContextAndCloseReportDurabilityWarning(t *testing.T) {
	for _, finalizer := range []string{"save-context", "close"} {
		t.Run(finalizer, func(t *testing.T) {
			db := NewDatabase(62)
			db.dbFile = filepath.Join(t.TempDir(), "database.json")
			db.initialized = true
			cause := errors.New("injected final durability warning")
			installPostRenameWarning(t, db, cause)

			var err error
			if finalizer == "save-context" {
				err = db.SaveContext(context.Background())
			} else {
				if setErr := db.Set("owner", "key", "value"); setErr != nil {
					t.Fatalf("Set returned a post-rename warning: %v", setErr)
				}
				err = db.Close(context.Background())
			}
			assertCommittedWarning(t, err, cause)
		})
	}
}

func TestDatabasePostRenameWarningSchedulesConfigReloadExactlyOnce(t *testing.T) {
	db := NewDatabase(63)
	db.dbFile = filepath.Join(t.TempDir(), "database.json")
	db.initialized = true
	client := NewCustomTelegramClient(63)
	client.GorokuDB = db
	client.Loader = NewModules(client, db)
	db.AttachRuntime(client.Loader, client.Loader, newTelegramAssetTransport(client))
	module := &registrationConfigModule{registrationLifecycleModule: &registrationLifecycleModule{name: "ConfigTarget"}}
	if err := client.Loader.RegisterModule(module); err != nil {
		t.Fatal(err)
	}
	module.configReady.Store(0)

	installPostRenameWarning(t, db, errors.New("injected config durability warning"))
	if err := db.Set(module.Name(), "enabled", false); err != nil {
		t.Fatalf("Set returned a post-rename warning: %v", err)
	}

	deadline := time.Now().Add(time.Second)
	for module.configReady.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	time.Sleep(20 * time.Millisecond)
	if calls := module.configReady.Load(); calls != 1 {
		t.Fatalf("ConfigReady calls = %d, want exactly one after logical commit", calls)
	}
}

func TestDatabaseRollbackTracksEveryMutationAndFirstWrite(t *testing.T) {
	db := NewDatabase(52)
	db.dbFile = filepath.Join(t.TempDir(), "database.json")
	db.initialized = true

	if err := db.Set("owner", "key", "first"); err != nil {
		t.Fatal(err)
	}
	if err := db.Rollback(); err != nil {
		t.Fatal(err)
	}
	if got, _ := db.Get("owner", "key", nil); got != nil {
		t.Fatalf("first-write rollback retained value %v", got)
	}

	if err := db.Set("owner", "key", "first"); err != nil {
		t.Fatal(err)
	}
	if err := db.Set("owner", "key", "second"); err != nil {
		t.Fatal(err)
	}
	if err := db.Set("owner", "key", "third"); err != nil {
		t.Fatal(err)
	}
	if err := db.Rollback(); err != nil {
		t.Fatal(err)
	}
	if got, _ := db.Get("owner", "key", nil); got != "second" {
		t.Fatalf("latest mutation rollback = %v, want second", got)
	}
}

func TestDatabaseFailedRollbackRetainsRevision(t *testing.T) {
	db := NewDatabase(53)
	db.dbFile = filepath.Join(t.TempDir(), "database.json")
	db.initialized = true
	if err := db.Set("owner", "key", "value"); err != nil {
		t.Fatal(err)
	}
	injected := errors.New("injected rollback persistence failure")
	db.writeLocal = func(string, []byte) error { return injected }
	if err := db.Rollback(); !errors.Is(err, injected) {
		t.Fatalf("Rollback error = %v", err)
	}
	if len(db.revisions) != 1 {
		t.Fatalf("failed rollback consumed revision, count = %d", len(db.revisions))
	}
	if got, _ := db.Get("owner", "key", nil); got != "value" {
		t.Fatalf("failed rollback changed memory to %v", got)
	}
}

func TestDatabaseRevisionCapKeepsLatestMutations(t *testing.T) {
	db := NewDatabase(54)
	db.dbFile = filepath.Join(t.TempDir(), "database.json")
	db.initialized = true
	for i := range 20 {
		if err := db.Set("owner", "key", i); err != nil {
			t.Fatal(err)
		}
	}
	if len(db.revisions) != 15 {
		t.Fatalf("revision count = %d, want 15", len(db.revisions))
	}
	for want := 18; want >= 4; want-- {
		if err := db.Rollback(); err != nil {
			t.Fatal(err)
		}
		if got := db.GetInt("owner", "key", -1); got != want {
			t.Fatalf("rollback value = %d, want %d", got, want)
		}
	}
	if err := db.Rollback(); !errors.Is(err, ErrDatabaseNoRevision) {
		t.Fatalf("Rollback beyond cap error = %v", err)
	}
}

func TestDatabaseCloseCallerDeadlinesAreIndependent(t *testing.T) {
	db := NewDatabase(55)
	db.dbFile = filepath.Join(t.TempDir(), "database.json")
	db.initialized = true
	db.persistMu.Lock()

	firstCtx, cancelFirst := context.WithCancel(context.Background())
	firstDone := make(chan error, 1)
	go func() { firstDone <- db.Close(firstCtx) }()
	for {
		db.mu.RLock()
		closing := db.closing
		db.mu.RUnlock()
		if closing {
			break
		}
		runtime.Gosched()
	}
	cancelFirst()
	if err := <-firstDone; !errors.Is(err, context.Canceled) {
		t.Fatalf("first Close error = %v", err)
	}

	secondDone := make(chan error, 1)
	go func() { secondDone <- db.Close(context.Background()) }()
	db.persistMu.Unlock()
	if err := <-secondDone; err != nil {
		t.Fatalf("second Close inherited first cancellation: %v", err)
	}
}

func TestDatabaseWriteStartedBeforeClosePublishesConsistently(t *testing.T) {
	db := NewDatabase(56)
	db.dbFile = filepath.Join(t.TempDir(), "database.json")
	db.initialized = true
	started := make(chan struct{})
	release := make(chan struct{})
	db.writeLocal = func(path string, data []byte) error {
		close(started)
		<-release
		return writeFileAtomic(path, data)
	}

	setDone := make(chan error, 1)
	go func() { setDone <- db.Set("owner", "key", "committed") }()
	<-started
	closeDone := make(chan error, 1)
	go func() { closeDone <- db.Close(context.Background()) }()
	close(release)
	if err := <-setDone; err != nil {
		t.Fatalf("in-flight Set failed: %v", err)
	}
	if err := <-closeDone; err != nil {
		t.Fatalf("Close failed: %v", err)
	}
	if got := db.Dump()["owner"]["key"]; got != "committed" {
		t.Fatalf("memory value = %v", got)
	}
	restarted := NewDatabase(56)
	restarted.dbFile = db.dbFile
	restarted.data, restarted.initialized = restarted.readLocal(db.dbFile)
	if got, _ := restarted.Get("owner", "key", nil); got != "committed" {
		t.Fatalf("restart value = %v", got)
	}
}

func TestDatabaseTypedSetterRetainsDefensiveCopy(t *testing.T) {
	db := NewDatabase(6)
	db.dbFile = filepath.Join(t.TempDir(), "database.json")
	db.initialized = true
	value := map[string][]string{"key": {"old"}}
	if err := db.SetStringMapStringSlice("owner", "key", value); err != nil {
		t.Fatal(err)
	}
	value["key"][0] = "caller mutation"
	got := db.GetStringMapStringSlice("owner", "key", nil)
	if !reflect.DeepEqual(got, map[string][]string{"key": {"old"}}) {
		t.Fatalf("typed setter retained caller alias: %#v", got)
	}
}

func TestDatabaseStageResetCommitsOnlyOnRename(t *testing.T) {
	dir := t.TempDir()
	db := NewDatabase(81)
	db.dbFile = filepath.Join(dir, "database.json")
	db.initialized = true
	if err := db.Set("owner", "key", "old"); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(db.dbFile)
	if err != nil {
		t.Fatal(err)
	}

	staged, err := db.StageReset(map[string]map[string]any{"owner": {"key": "staged"}})
	if err != nil {
		t.Fatal(err)
	}
	if staged == "" {
		t.Fatal("empty staged path")
	}
	if _, err := os.Stat(staged); err != nil {
		t.Fatalf("staged missing: %v", err)
	}
	primary, err := os.ReadFile(db.dbFile)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(primary, before) {
		t.Fatal("StageReset renamed onto primary")
	}
	if got := db.GetString("owner", "key", ""); got != "old" {
		t.Fatalf("memory after stage = %q, want old", got)
	}

	if err := db.CommitStagedReset(staged, map[string]map[string]any{"owner": {"key": "staged"}}); err != nil {
		t.Fatal(err)
	}
	if got := db.GetString("owner", "key", ""); got != "staged" {
		t.Fatalf("memory after commit = %q, want staged", got)
	}
	if _, err := os.Stat(staged); !os.IsNotExist(err) {
		t.Fatalf("staged path still present after commit: %v", err)
	}
	if _, err := os.Stat(lastValidPath(db.dbFile)); err != nil {
		t.Fatalf("last-valid missing after staged commit: %v", err)
	}
}

func TestDatabaseAbortStagedReset(t *testing.T) {
	dir := t.TempDir()
	db := NewDatabase(82)
	db.dbFile = filepath.Join(dir, "database.json")
	db.initialized = true
	if err := db.Set("owner", "key", "old"); err != nil {
		t.Fatal(err)
	}
	staged, err := db.StageReset(map[string]map[string]any{"owner": {"key": "gone"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AbortStagedReset(staged); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(staged); !os.IsNotExist(err) {
		t.Fatalf("staged still present after abort: %v", err)
	}
	if got := db.GetString("owner", "key", ""); got != "old" {
		t.Fatalf("memory after abort = %q", got)
	}
}

func TestWriteAndReadRestoreCommitMarker(t *testing.T) {
	dir := t.TempDir()
	dbFile := filepath.Join(dir, "config-1.json")
	if err := WriteRestoreCommitMarker(dbFile, "abc123"); err != nil {
		t.Fatal(err)
	}
	if got := ReadRestoreCommitMarker(dbFile); got != "abc123" {
		t.Fatalf("marker = %q, want abc123", got)
	}
	primaryMarker := dbFile + ".restore-id"
	lastValidMarker := lastValidPath(dbFile) + ".restore-id"
	if _, err := os.Stat(primaryMarker); err != nil {
		t.Fatalf("primary marker path missing: %v", err)
	}
	if _, err := os.Stat(lastValidMarker); err != nil {
		t.Fatalf("last-valid marker path missing: %v", err)
	}
	// Primary marker alone is enough when last-valid sibling is gone.
	if err := os.Remove(lastValidMarker); err != nil {
		t.Fatal(err)
	}
	if got := ReadRestoreCommitMarker(dbFile); got != "abc123" {
		t.Fatalf("primary-only marker = %q, want abc123", got)
	}
}

func TestCommitStagedDatabaseFilePreservesSource(t *testing.T) {
	dir := t.TempDir()
	dbFile := filepath.Join(dir, "database.json")
	if err := os.WriteFile(dbFile, []byte(`{"owner":{"key":"old"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	staged := filepath.Join(dir, "retained-staged.json")
	body := []byte(`{"owner":{"key":"new"},"goroku.restore":{"restore_id":"rid-1"}}`)
	if err := os.WriteFile(staged, body, 0600); err != nil {
		t.Fatal(err)
	}
	if err := CommitStagedDatabaseFile(dbFile, staged); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(staged); err != nil {
		t.Fatalf("retained staged removed: %v", err)
	}
	got, err := os.ReadFile(dbFile)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(got, []byte(`"key": "new"`)) && !bytes.Contains(got, []byte(`"key":"new"`)) {
		// Marshal from stageLocalCandidate re-encodes via commit of raw body.
		if !bytes.Contains(got, []byte("new")) {
			t.Fatalf("primary after offline commit = %s", got)
		}
	}
}

func TestDatabaseRetainsLastValidAfterSuccessfulWrite(t *testing.T) {
	dir := t.TempDir()
	db := NewDatabase(80)
	db.dbFile = filepath.Join(dir, "database.json")
	db.initialized = true
	if err := db.Set("owner", "key", "gen1"); err != nil {
		t.Fatal(err)
	}
	if err := db.Set("owner", "key", "gen2"); err != nil {
		t.Fatal(err)
	}

	lastValid := lastValidPath(db.dbFile)
	content, err := os.ReadFile(lastValid)
	if err != nil {
		t.Fatalf("last-valid missing after successful write: %v", err)
	}
	var previous map[string]map[string]any
	if err := json.Unmarshal(content, &previous); err != nil {
		t.Fatalf("last-valid is invalid JSON: %v", err)
	}
	if got := previous["owner"]["key"]; got != "gen1" {
		t.Fatalf("last-valid value = %v, want gen1", got)
	}

	primary, err := os.ReadFile(db.dbFile)
	if err != nil {
		t.Fatal(err)
	}
	var current map[string]map[string]any
	if err := json.Unmarshal(primary, &current); err != nil {
		t.Fatal(err)
	}
	if got := current["owner"]["key"]; got != "gen2" {
		t.Fatalf("primary value = %v, want gen2", got)
	}

	temps, err := filepath.Glob(filepath.Join(dir, ".goroku-db-*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(temps) != 0 {
		t.Fatalf("atomic save left temporary files: %v", temps)
	}
}

func TestDatabaseWriteFailurePreservesPrimaryAndLastValid(t *testing.T) {
	dir := t.TempDir()
	db := NewDatabase(81)
	db.dbFile = filepath.Join(dir, "database.json")
	db.initialized = true
	if err := db.Set("owner", "key", "gen1"); err != nil {
		t.Fatal(err)
	}
	if err := db.Set("owner", "key", "gen2"); err != nil {
		t.Fatal(err)
	}
	beforePrimary, err := os.ReadFile(db.dbFile)
	if err != nil {
		t.Fatal(err)
	}
	beforeLastValid, err := os.ReadFile(lastValidPath(db.dbFile))
	if err != nil {
		t.Fatal(err)
	}

	ops := defaultAtomicFileOps
	ops.rename = func(string, string) error { return errors.New("injected rename failure") }
	db.writeLocal = func(path string, data []byte) error {
		return writeFileAtomicWithOps(path, data, ops)
	}
	if err := db.Set("owner", "key", "gen3"); !errors.Is(err, ErrDatabasePersistence) {
		t.Fatalf("expected persistence failure, got %v", err)
	}

	afterPrimary, err := os.ReadFile(db.dbFile)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(afterPrimary, beforePrimary) {
		t.Fatalf("primary changed after failed write:\n before=%s\n after=%s", beforePrimary, afterPrimary)
	}
	afterLastValid, err := os.ReadFile(lastValidPath(db.dbFile))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(afterLastValid, beforeLastValid) {
		t.Fatalf("last-valid changed after failed write")
	}
	if got := db.Dump()["owner"]["key"]; got != "gen2" {
		t.Fatalf("memory published failed write: %v", got)
	}
}

func TestDatabaseInitRecoversFromLastValid(t *testing.T) {
	tempDir := t.TempDir()
	originalBaseDir := BaseDir
	BaseDir = tempDir
	defer func() { BaseDir = originalBaseDir }()

	tgID := int64(82)
	path := filepath.Join(tempDir, fmt.Sprintf("config-%d.json", tgID))
	good := []byte(`{"owner":{"key":"recovered-value"}}`)
	if err := os.WriteFile(path+".last-valid", good, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{not-json`), 0600); err != nil {
		t.Fatal(err)
	}

	db := NewDatabase(tgID)
	if err := db.Init(""); err != nil {
		t.Fatalf("Init failed: %v", err)
	}
	defer db.Close(context.Background())
	if got := db.GetString("owner", "key", ""); got != "recovered-value" {
		t.Fatalf("recovered value = %q", got)
	}
	repaired, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var parsed map[string]map[string]any
	if err := json.Unmarshal(repaired, &parsed); err != nil {
		t.Fatalf("primary not repaired: %v", err)
	}
	if got := parsed["owner"]["key"]; got != "recovered-value" {
		t.Fatalf("repaired primary = %v", got)
	}
}

func TestDatabaseInitRejectsCorruptWithoutBackup(t *testing.T) {
	tempDir := t.TempDir()
	originalBaseDir := BaseDir
	BaseDir = tempDir
	defer func() { BaseDir = originalBaseDir }()

	tgID := int64(83)
	path := filepath.Join(tempDir, fmt.Sprintf("config-%d.json", tgID))
	if err := os.WriteFile(path, []byte(`{not-json`), 0600); err != nil {
		t.Fatal(err)
	}

	db := NewDatabase(tgID)
	err := db.Init("")
	if err == nil {
		t.Fatal("Init silently accepted corrupt primary without backup")
	}
	if !errors.Is(err, ErrDatabaseCorrupt) {
		t.Fatalf("Init error = %v, want ErrDatabaseCorrupt", err)
	}
}

func TestDatabaseLastValidPreservedWhenRetentionInstallFails(t *testing.T) {
	dir := t.TempDir()
	db := NewDatabase(84)
	db.dbFile = filepath.Join(dir, "database.json")
	db.initialized = true
	if err := db.Set("owner", "key", "gen1"); err != nil {
		t.Fatal(err)
	}
	if err := db.Set("owner", "key", "gen2"); err != nil {
		t.Fatal(err)
	}
	beforeLastValid, err := os.ReadFile(lastValidPath(db.dbFile))
	if err != nil {
		t.Fatalf("seed last-valid: %v", err)
	}
	var previous map[string]map[string]any
	if err := json.Unmarshal(beforeLastValid, &previous); err != nil {
		t.Fatal(err)
	}
	if previous["owner"]["key"] != "gen1" {
		t.Fatalf("seed last-valid = %v, want gen1", previous["owner"]["key"])
	}

	// Primary publish succeeds; every install into *.last-valid fails (direct
	// rename and temp+rename fallback), so the live last-valid sibling must
	// remain readable and byte-identical.
	ops := defaultAtomicFileOps
	ops.rename = func(oldpath, newpath string) error {
		if strings.HasSuffix(newpath, ".last-valid") {
			return errors.New("injected last-valid install failure")
		}
		return defaultAtomicFileOps.rename(oldpath, newpath)
	}
	db.writeLocal = func(path string, data []byte) error {
		return writeFileAtomicWithOps(path, data, ops)
	}

	if err := db.Set("owner", "key", "gen3"); err != nil {
		t.Fatalf("logical Set after primary publish should succeed: %v", err)
	}
	assertCommittedWarning(t, db.DurabilityWarning(), nil)

	primary, err := os.ReadFile(db.dbFile)
	if err != nil {
		t.Fatal(err)
	}
	var current map[string]map[string]any
	if err := json.Unmarshal(primary, &current); err != nil {
		t.Fatal(err)
	}
	if current["owner"]["key"] != "gen3" {
		t.Fatalf("primary = %v, want gen3", current["owner"]["key"])
	}

	afterLastValid, err := os.ReadFile(lastValidPath(db.dbFile))
	if err != nil {
		t.Fatalf("last-valid unreadable after failed retention: %v", err)
	}
	if !reflect.DeepEqual(afterLastValid, beforeLastValid) {
		t.Fatalf("last-valid clobbered by failed retention fallback:\n before=%s\n after=%s",
			beforeLastValid, afterLastValid)
	}
	if got := db.Dump()["owner"]["key"]; got != "gen3" {
		t.Fatalf("memory value = %v, want gen3", got)
	}
}
