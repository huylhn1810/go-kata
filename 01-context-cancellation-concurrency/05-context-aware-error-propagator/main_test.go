package main

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"go.uber.org/mock/gomock"
)

// Self-Correction 1: "The Sensitive Data Leak"
// Force an auth error with a mock API key.
// Fail Condition: If fmt.Sprint(err) contains the API key string.
func TestSensitiveDataLeak(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	apiKey := "super-secret-api-key-12345"

	mockAuth := NewMockAuthService(ctrl)
	mockMeta := NewMockMetadataService(ctrl)
	mockStorage := NewMockStorageService(ctrl)

	// Auth trả về lỗi có chứa API key (simulate leak attempt)
	mockAuth.EXPECT().
		Authentication(gomock.Any(), gomock.Any()).
		Return(&AuthError{
			userID: 1,
			err:    fmt.Errorf("invalid token"),
		})

	svc := NewGateWayService(mockAuth, mockMeta, mockStorage)
	err := svc.Upload(1, apiKey, "/path/to/file")

	if err == nil {
		t.Fatal("expected error, got nil")
	}

	// Fail condition: error string không được chứa API key
	if strings.Contains(fmt.Sprint(err), apiKey) {
		t.Errorf("FAIL: error message leaks sensitive API key: %v", err)
	}

	t.Logf("PASS: error message is safe: %v", err)
}

// Self-Correction 2: "The Lost Context"
// Wrap an AuthError three times through different layers.
// Fail Condition: If errors.As(err, &AuthError{}) returns false.
func TestLostContext(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockAuth := NewMockAuthService(ctrl)
	mockMeta := NewMockMetadataService(ctrl)
	mockStorage := NewMockStorageService(ctrl)

	originalAuthErr := &AuthError{
		userID: 42,
		err:    errors.New("unauthorized"),
	}

	// Auth layer trả về AuthError
	mockAuth.EXPECT().
		Authentication(gomock.Any(), gomock.Any()).
		Return(originalAuthErr)

	svc := NewGateWayService(mockAuth, mockMeta, mockStorage)
	err := svc.Upload(42, "token", "/upload")

	if err == nil {
		t.Fatal("expected error, got nil")
	}

	// Wrap thêm 3 lần qua 3 layer khác nhau
	wrapped := fmt.Errorf("layer1: %w", err)
	wrapped = fmt.Errorf("layer2: %w", wrapped)
	wrapped = fmt.Errorf("layer3: %w", wrapped)

	// Fail condition: errors.As phải tìm thấy *AuthError dù đã wrap nhiều lần
	var authErr *AuthError
	if !errors.As(wrapped, &authErr) {
		t.Errorf("FAIL: errors.As could not find *AuthError after 3 wraps: %v", wrapped)
	} else {
		t.Logf("PASS: errors.As found *AuthError with userID=%d", authErr.userID)
	}
}

// Self-Correction 3: "The Timeout Confusion"
// Create a timeout error in the storage layer.
// Fail Condition: If the StorageError wrapping ErrTimeOut does not expose Timeout() == true.
func TestTimeoutConfusion(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockAuth := NewMockAuthService(ctrl)
	mockMeta := NewMockMetadataService(ctrl)
	mockStorage := NewMockStorageService(ctrl)

	storageTimeoutErr := &StorageError{
		userID: 7,
		err:    ErrTimeout,
	}

	// Auth và Metadata thành công
	mockAuth.EXPECT().
		Authentication(gomock.Any(), gomock.Any()).
		Return(nil)
	mockMeta.EXPECT().
		Save(gomock.Any(), gomock.Any()).
		Return(nil)
	// Storage trả về timeout error
	mockStorage.EXPECT().
		Upload(gomock.Any(), gomock.Any()).
		Return(storageTimeoutErr)

	svc := NewGateWayService(mockAuth, mockMeta, mockStorage)
	err := svc.Upload(7, "token", "/upload")

	if err == nil {
		t.Fatal("expected error, got nil")
	}

	// Fail condition: phải unwrap được *StorageError và Timeout() phải là true
	var storageErr *StorageError
	if !errors.As(err, &storageErr) {
		t.Errorf("FAIL: errors.As could not find *StorageError: %v", err)
		return
	}

	if !storageErr.Timeout() {
		t.Errorf("FAIL: StorageError.Timeout() returned false, expected true")
	} else {
		t.Logf("PASS: StorageError.Timeout() = true, ErrTimeOut is preserved")
	}
}
