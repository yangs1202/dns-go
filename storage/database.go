package storage

import (
	"database/sql"
	"fmt"

	_ "modernc.org/sqlite"
)

// Database는 SQLite 데이터베이스 연결을 관리합니다
type Database struct {
	Writer *sql.DB // 쓰기 전용 연결
	Reader *sql.DB // 읽기 전용 연결
}

// NewDatabase는 새로운 데이터베이스 연결을 생성합니다
func NewDatabase(path string) (*Database, error) {
	// Writer 연결 (단일 연결)
	writer, err := sql.Open("sqlite", path+"?_journal_mode=DELETE&_synchronous=FULL&_foreign_keys=ON")
	if err != nil {
		return nil, fmt.Errorf("writer 연결 실패: %w", err)
	}
	writer.SetMaxOpenConns(1) // SQLite 쓰기 직렬화

	// Reader 연결 (다중 연결)
	reader, err := sql.Open("sqlite", path+"?_journal_mode=DELETE&_synchronous=FULL&_foreign_keys=ON")
	if err != nil {
		writer.Close()
		return nil, fmt.Errorf("reader 연결 실패: %w", err)
	}
	reader.SetMaxOpenConns(4) // 동시 읽기 지원

	// 연결 테스트
	if err := writer.Ping(); err != nil {
		writer.Close()
		reader.Close()
		return nil, fmt.Errorf("writer ping 실패: %w", err)
	}
	if err := reader.Ping(); err != nil {
		writer.Close()
		reader.Close()
		return nil, fmt.Errorf("reader ping 실패: %w", err)
	}

	db := &Database{
		Writer: writer,
		Reader: reader,
	}

	// WAL 모드 및 외래 키 활성화 확인
	if err := db.configurePragmas(); err != nil {
		db.Close()
		return nil, fmt.Errorf("PRAGMA 설정 실패: %w", err)
	}

	// 마이그레이션 실행
	if err := db.Migrate(); err != nil {
		db.Close()
		return nil, fmt.Errorf("마이그레이션 실패: %w", err)
	}

	return db, nil
}

// configurePragmas는 SQLite PRAGMA 설정을 확인합니다
func (db *Database) configurePragmas() error {
	// 외래 키 활성화
	if _, err := db.Writer.Exec("PRAGMA foreign_keys=ON"); err != nil {
		return fmt.Errorf("외래 키 활성화 실패: %w", err)
	}

	// Reader도 동일 설정
	if _, err := db.Reader.Exec("PRAGMA foreign_keys=ON"); err != nil {
		return fmt.Errorf("reader 외래 키 활성화 실패: %w", err)
	}

	return nil
}

// Close는 데이터베이스 연결을 닫습니다
func (db *Database) Close() error {
	var err error

	if db.Writer != nil {
		if e := db.Writer.Close(); e != nil {
			err = e
		}
	}
	if db.Reader != nil {
		if e := db.Reader.Close(); e != nil {
			err = e
		}
	}
	return err
}
