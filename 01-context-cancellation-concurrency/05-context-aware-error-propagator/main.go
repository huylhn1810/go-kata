package main

//go:generate mockgen -source=main.go -destination=mock_main_test.go -package=main

import (
	"errors"
	"fmt"
)

var (
	ErrTimeout   = errors.New("timeout")
	ErrTemporary = errors.New("temporary")
)

// Timeout
type Timeout interface {
	Timeout() bool
}

// Temporary
type Temporary interface {
	Temporary() bool
}

// Auth
type AuthService interface {
	Authentication(userID int, token string) error
}

type authService struct{}

func NewAuthService() AuthService {
	return &authService{}
}

func (as *authService) Authentication(userID int, token string) error {
	return nil
}

// AuthError
type AuthError struct {
	userID int
	err    error
}

func (ae *AuthError) Error() string {
	if ae.err != nil {
		return fmt.Sprintf("%v userID %d", ae.err.Error(), ae.userID)
	}

	return ""
}

func (ae *AuthError) Unwrap() error {
	return ae.err
}

func (ae *AuthError) Timeout() bool {
	return ae.err == ErrTimeout
}

func (ae *AuthError) Temporary() bool {
	return ae.err == ErrTemporary
}

// Metadata
type MetadataService interface {
	Save(userID int, urlPath string) error
}

type metadataService struct{}

func NewMetadataService() MetadataService {
	return &metadataService{}
}

func (ms *metadataService) Save(userID int, urlPath string) error {
	return nil
}

// MetadataError
type MetadataError struct {
	userID int
	err    error
}

func (me *MetadataError) Error() string {
	if me.err != nil {
		return fmt.Sprintf("%v userID %d", me.err.Error(), me.userID)
	}

	return ""
}

func (me *MetadataError) Unwrap() error {
	return me.err
}

func (me *MetadataError) Timeout() bool {
	return me.err == ErrTimeout
}

func (me *MetadataError) Temporary() bool {
	return me.err == ErrTemporary
}

// Storage
type StorageService interface {
	Upload(userID int, urlPath string) error
}

type storageService struct{}

func NewStorageService() StorageService {
	return &storageService{}
}

func (ss *storageService) Upload(userID int, urlPath string) error {
	return nil
}

// StorageError
type StorageError struct {
	userID int
	err    error
}

func (se *StorageError) Error() string {
	if se.err != nil {
		return fmt.Sprintf("%v userID %d", se.err.Error(), se.userID)
	}

	return ""
}

func (se *StorageError) Unwrap() error {
	return se.err
}

func (se *StorageError) Timeout() bool {
	return se.err == ErrTimeout
}

func (se *StorageError) Temporary() bool {
	return se.err == ErrTemporary
}

type GateWayService interface {
	Upload(userID int, token string, urlPath string) error
}

type gatewayService struct {
	authService     AuthService
	metadataService MetadataService
	storageService  StorageService
}

func NewGateWayService(auth AuthService, meta MetadataService, storage StorageService) GateWayService {
	return &gatewayService{
		authService:     auth,
		metadataService: meta,
		storageService:  storage,
	}
}

func (gs *gatewayService) Upload(userID int, token string, urlPath string) error {
	if err := gs.authService.Authentication(userID, token); err != nil {
		return fmt.Errorf("auth failed: %w", err)
	}

	if err := gs.metadataService.Save(userID, urlPath); err != nil {
		return fmt.Errorf("metadata save failed: %w", err)
	}

	if err := gs.storageService.Upload(userID, urlPath); err != nil {
		return fmt.Errorf("blob upload failed: %w", err)
	}

	return nil
}
