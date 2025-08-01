package storage

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// TestMemoryStorage_Put tests the Put method of MemoryStorage
func TestMemoryStorage_Put(t *testing.T) {
	storage := NewMemoryStorage()

	// Test putting a simple string
	err := storage.Put("key1", "value1")
	if err != nil {
		t.Errorf("Put failed: %v", err)
	}

	// Test putting different types
	err = storage.Put("key2", 123)
	if err != nil {
		t.Errorf("Put failed: %v", err)
	}

	err = storage.Put("key3", map[string]string{"test": "data"})
	if err != nil {
		t.Errorf("Put failed: %v", err)
	}
}

// TestMemoryStorage_Get tests the Get method of MemoryStorage
func TestMemoryStorage_Get(t *testing.T) {
	storage := NewMemoryStorage()

	// Put some test data
	storage.Put("key1", "value1")
	storage.Put("key2", 123)

	// Test getting existing key
	value, err := storage.Get("key1")
	if err != nil {
		t.Errorf("Get failed: %v", err)
	}
	if value != "value1" {
		t.Errorf("Expected 'value1', got %v", value)
	}

	// Test getting another existing key
	value, err = storage.Get("key2")
	if err != nil {
		t.Errorf("Get failed: %v", err)
	}
	if value != 123 {
		t.Errorf("Expected 123, got %v", value)
	}

	// Test getting non-existent key
	_, err = storage.Get("nonexistent")
	if err != ErrNotFound {
		t.Errorf("Expected ErrNotFound, got %v", err)
	}
}

// TestMemoryStorage_List tests the List method of MemoryStorage
func TestMemoryStorage_List(t *testing.T) {
	storage := NewMemoryStorage()

	// Test empty storage
	values, err := storage.List()
	if err != nil {
		t.Errorf("List failed: %v", err)
	}
	if len(values) != 0 {
		t.Errorf("Expected empty list, got %d items", len(values))
	}

	// Add some test data
	storage.Put("key1", "value1")
	storage.Put("key2", "value2")
	storage.Put("key3", "value3")

	// Test listing all values
	values, err = storage.List()
	if err != nil {
		t.Errorf("List failed: %v", err)
	}
	if len(values) != 3 {
		t.Errorf("Expected 3 items, got %d", len(values))
	}

	// Check if all values are present
	expectedValues := map[string]bool{"value1": false, "value2": false, "value3": false}
	for _, value := range values {
		if str, ok := value.(string); ok {
			expectedValues[str] = true
		}
	}

	for value, found := range expectedValues {
		if !found {
			t.Errorf("Expected value %s not found in list", value)
		}
	}
}

// TestMemoryStorage_Delete tests the Delete method of MemoryStorage
func TestMemoryStorage_Delete(t *testing.T) {
	storage := NewMemoryStorage()

	// Add test data
	storage.Put("key1", "value1")
	storage.Put("key2", "value2")

	// Test deleting existing key
	err := storage.Delete("key1")
	if err != nil {
		t.Errorf("Delete failed: %v", err)
	}

	// Verify the key is deleted
	_, err = storage.Get("key1")
	if err != ErrNotFound {
		t.Errorf("Expected ErrNotFound after delete, got %v", err)
	}

	// Verify other key still exists
	value, err := storage.Get("key2")
	if err != nil {
		t.Errorf("Get failed: %v", err)
	}
	if value != "value2" {
		t.Errorf("Expected 'value2', got %v", value)
	}

	// Test deleting non-existent key (should not error)
	err = storage.Delete("nonexistent")
	if err != nil {
		t.Errorf("Delete of non-existent key should not error: %v", err)
	}
}

// TestMemoryStorage_Concurrency tests concurrent access to MemoryStorage
func TestMemoryStorage_Concurrency(t *testing.T) {
	storage := NewMemoryStorage()
	done := make(chan bool, 10)

	// Start multiple goroutines writing
	for i := 0; i < 5; i++ {
		go func(id int) {
			for j := 0; j < 100; j++ {
				key := fmt.Sprintf("key_%d_%d", id, j)
				value := fmt.Sprintf("value_%d_%d", id, j)
				storage.Put(key, value)
			}
			done <- true
		}(i)
	}

	// Start multiple goroutines reading
	for i := 0; i < 5; i++ {
		go func(id int) {
			for j := 0; j < 100; j++ {
				key := fmt.Sprintf("key_%d_%d", id, j)
				storage.Get(key)
			}
			done <- true
		}(i)
	}

	// Wait for all goroutines to complete
	for i := 0; i < 10; i++ {
		<-done
	}

	// Verify final state
	values, err := storage.List()
	if err != nil {
		t.Errorf("List failed: %v", err)
	}
	if len(values) != 500 {
		t.Errorf("Expected 500 items, got %d", len(values))
	}
}

// TestDiskStorage_NewDiskStorage tests creating new disk storage
func TestDiskStorage_NewDiskStorage(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test.db")

	// Test JSON serializer/deserializer
	serializer := func(v interface{}) ([]byte, error) {
		return json.Marshal(v)
	}
	deserializer := func(data []byte) (interface{}, error) {
		var result interface{}
		err := json.Unmarshal(data, &result)
		return result, err
	}

	storage, err := NewDiskStorage("test_bucket", dbPath, serializer, deserializer)
	if err != nil {
		t.Fatalf("NewDiskStorage failed: %v", err)
	}

	if storage == nil {
		t.Fatal("Expected non-nil storage")
	}

	// Verify the database file was created
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		t.Error("Database file was not created")
	}
}

// TestDiskStorage_PutGet tests Put and Get operations on disk storage
func TestDiskStorage_PutGet(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test.db")

	serializer := func(v interface{}) ([]byte, error) {
		return json.Marshal(v)
	}
	deserializer := func(data []byte) (interface{}, error) {
		var result interface{}
		err := json.Unmarshal(data, &result)
		return result, err
	}

	storage, err := NewDiskStorage("test_bucket", dbPath, serializer, deserializer)
	if err != nil {
		t.Fatalf("NewDiskStorage failed: %v", err)
	}

	// Test putting and getting simple values
	testData := map[string]interface{}{
		"string": "test_string",
		"number": 123,
		"bool":   true,
		"array":  []string{"a", "b", "c"},
		"object": map[string]string{"key": "value"},
	}

	for key, value := range testData {
		err := storage.Put(key, value)
		if err != nil {
			t.Errorf("Put failed for key %s: %v", key, err)
		}

		retrieved, err := storage.Get(key)
		if err != nil {
			t.Errorf("Get failed for key %s: %v", key, err)
		}

		// Compare the values (they should be equal after JSON marshaling/unmarshaling)
		expectedJSON, _ := json.Marshal(value)
		actualJSON, _ := json.Marshal(retrieved)
		if string(expectedJSON) != string(actualJSON) {
			t.Errorf("Value mismatch for key %s: expected %v, got %v", key, value, retrieved)
		}
	}
}

// TestDiskStorage_List tests List operation on disk storage
func TestDiskStorage_List(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test.db")

	serializer := func(v interface{}) ([]byte, error) {
		return json.Marshal(v)
	}
	deserializer := func(data []byte) (interface{}, error) {
		var result interface{}
		err := json.Unmarshal(data, &result)
		return result, err
	}

	storage, err := NewDiskStorage("test_bucket", dbPath, serializer, deserializer)
	if err != nil {
		t.Fatalf("NewDiskStorage failed: %v", err)
	}

	// Test empty storage
	values, err := storage.List()
	if err != nil {
		t.Errorf("List failed: %v", err)
	}
	if len(values) != 0 {
		t.Errorf("Expected empty list, got %d items", len(values))
	}

	// Add test data
	testData := []string{"value1", "value2", "value3"}
	for i, value := range testData {
		key := fmt.Sprintf("key%d", i)
		storage.Put(key, value)
	}

	// Test listing all values
	values, err = storage.List()
	if err != nil {
		t.Errorf("List failed: %v", err)
	}
	if len(values) != 3 {
		t.Errorf("Expected 3 items, got %d", len(values))
	}

	// Check if all values are present
	expectedValues := map[string]bool{"value1": false, "value2": false, "value3": false}
	for _, value := range values {
		if str, ok := value.(string); ok {
			expectedValues[str] = true
		}
	}

	for value, found := range expectedValues {
		if !found {
			t.Errorf("Expected value %s not found in list", value)
		}
	}
}

// TestDiskStorage_Delete tests Delete operation on disk storage
func TestDiskStorage_Delete(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test.db")

	serializer := func(v interface{}) ([]byte, error) {
		return json.Marshal(v)
	}
	deserializer := func(data []byte) (interface{}, error) {
		var result interface{}
		err := json.Unmarshal(data, &result)
		return result, err
	}

	storage, err := NewDiskStorage("test_bucket", dbPath, serializer, deserializer)
	if err != nil {
		t.Fatalf("NewDiskStorage failed: %v", err)
	}

	// Add test data
	storage.Put("key1", "value1")
	storage.Put("key2", "value2")

	// Test deleting existing key
	err = storage.Delete("key1")
	if err != nil {
		t.Errorf("Delete failed: %v", err)
	}

	// Verify the key is deleted
	_, err = storage.Get("key1")
	if err != ErrNotFound {
		t.Errorf("Expected ErrNotFound after delete, got %v", err)
	}

	// Verify other key still exists
	value, err := storage.Get("key2")
	if err != nil {
		t.Errorf("Get failed: %v", err)
	}
	if value != "value2" {
		t.Errorf("Expected 'value2', got %v", value)
	}

	// Test deleting non-existent key (should not error)
	err = storage.Delete("nonexistent")
	if err != nil {
		t.Errorf("Delete of non-existent key should not error: %v", err)
	}
}

// TestDiskStorage_Persistence tests that data persists across storage instances
func TestDiskStorage_Persistence(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test.db")

	serializer := func(v interface{}) ([]byte, error) {
		return json.Marshal(v)
	}
	deserializer := func(data []byte) (interface{}, error) {
		var result interface{}
		err := json.Unmarshal(data, &result)
		return result, err
	}

	// Create first storage instance and add data
	storage1, err := NewDiskStorage("test_bucket", dbPath, serializer, deserializer)
	if err != nil {
		t.Fatalf("NewDiskStorage failed: %v", err)
	}

	storage1.Put("key1", "value1")
	storage1.Put("key2", "value2")

	// Close the first storage (this would happen in real usage)
	if diskStorage, ok := storage1.(*DiskStorage); ok {
		diskStorage.db.Close()
	}

	// Create second storage instance and verify data persists
	storage2, err := NewDiskStorage("test_bucket", dbPath, serializer, deserializer)
	if err != nil {
		t.Fatalf("NewDiskStorage failed: %v", err)
	}

	// Verify data is loaded from disk
	value, err := storage2.Get("key1")
	if err != nil {
		t.Errorf("Get failed: %v", err)
	}
	if value != "value1" {
		t.Errorf("Expected 'value1', got %v", value)
	}

	value, err = storage2.Get("key2")
	if err != nil {
		t.Errorf("Get failed: %v", err)
	}
	if value != "value2" {
		t.Errorf("Expected 'value2', got %v", value)
	}
}

// TestDiskStorage_SerializerError tests error handling in serializer
func TestDiskStorage_SerializerError(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test.db")

	// Create a serializer that always fails
	failingSerializer := func(v interface{}) ([]byte, error) {
		return nil, fmt.Errorf("serialization failed")
	}
	deserializer := func(data []byte) (interface{}, error) {
		var result interface{}
		err := json.Unmarshal(data, &result)
		return result, err
	}

	storage, err := NewDiskStorage("test_bucket", dbPath, failingSerializer, deserializer)
	if err != nil {
		t.Fatalf("NewDiskStorage failed: %v", err)
	}

	// Test that Put fails when serializer fails
	err = storage.Put("key1", "value1")
	if err == nil {
		t.Error("Expected Put to fail when serializer fails")
	}
}

// TestDiskStorage_DeserializerError tests error handling in deserializer
func TestDiskStorage_DeserializerError(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test.db")

	serializer := func(v interface{}) ([]byte, error) {
		return json.Marshal(v)
	}
	// Create a deserializer that always fails
	failingDeserializer := func(data []byte) (interface{}, error) {
		return nil, fmt.Errorf("deserialization failed")
	}

	storage, err := NewDiskStorage("test_bucket", dbPath, serializer, failingDeserializer)
	if err != nil {
		t.Fatalf("NewDiskStorage failed: %v", err)
	}

	// Add some data
	storage.Put("key1", "value1")

	// Close and reopen to trigger load from disk
	if diskStorage, ok := storage.(*DiskStorage); ok {
		diskStorage.db.Close()
	}

	// This should fail during load due to deserializer error
	_, err = NewDiskStorage("test_bucket", dbPath, serializer, failingDeserializer)
	if err == nil {
		t.Error("Expected NewDiskStorage to fail when deserializer fails")
	}
}

// BenchmarkMemoryStorage_Put benchmarks Put operation on MemoryStorage
func BenchmarkMemoryStorage_Put(b *testing.B) {
	storage := NewMemoryStorage()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		key := fmt.Sprintf("key_%d", i)
		value := fmt.Sprintf("value_%d", i)
		storage.Put(key, value)
	}
}

// BenchmarkMemoryStorage_Get benchmarks Get operation on MemoryStorage
func BenchmarkMemoryStorage_Get(b *testing.B) {
	storage := NewMemoryStorage()

	// Pre-populate with data
	for i := 0; i < 1000; i++ {
		key := fmt.Sprintf("key_%d", i)
		value := fmt.Sprintf("value_%d", i)
		storage.Put(key, value)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		key := fmt.Sprintf("key_%d", i%1000)
		storage.Get(key)
	}
}

// BenchmarkDiskStorage_Put benchmarks Put operation on DiskStorage
func BenchmarkDiskStorage_Put(b *testing.B) {
	tempDir := b.TempDir()
	dbPath := filepath.Join(tempDir, "benchmark.db")

	serializer := func(v interface{}) ([]byte, error) {
		return json.Marshal(v)
	}
	deserializer := func(data []byte) (interface{}, error) {
		var result interface{}
		err := json.Unmarshal(data, &result)
		return result, err
	}

	storage, err := NewDiskStorage("benchmark_bucket", dbPath, serializer, deserializer)
	if err != nil {
		b.Fatalf("NewDiskStorage failed: %v", err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		key := fmt.Sprintf("key_%d", i)
		value := fmt.Sprintf("value_%d", i)
		storage.Put(key, value)
	}
}
