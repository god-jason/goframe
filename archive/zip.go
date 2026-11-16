package archive

import (
	"archive/zip"
	"fmt"
	"io"
	"os"
	"strings"
)

// listZip 读取 zip 条目。
func listZip(r io.ReaderAt, size int64) ([]Entry, error) {
	reader, err := zip.NewReader(r, size)
	if err != nil {
		return nil, fmt.Errorf("archive: open zip: %w", err)
	}

	entries := make([]Entry, 0, len(reader.File))
	for _, file := range reader.File {
		entries = append(entries, zipEntry(file))
	}
	return entries, nil
}

// extractZip 解压 zip。
func extractZip(r io.ReaderAt, size int64, dest string, opts extractOptions) error {
	reader, err := zip.NewReader(r, size)
	if err != nil {
		return fmt.Errorf("archive: open zip: %w", err)
	}
	ex, err := newExtractor(dest, opts)
	if err != nil {
		return err
	}

	for _, file := range reader.File {
		entry := zipEntry(file)
		switch {
		case entry.IsSymlink():
			linkTarget, err := readZipSymlink(file)
			if err != nil {
				return err
			}
			if err := ex.symlink(entry, linkTarget); err != nil {
				return err
			}
		case entry.IsDir:
			if err := ex.dir(entry); err != nil {
				return err
			}
		default:
			content, err := file.Open()
			if err != nil {
				return fmt.Errorf("archive: open zip entry %s: %w", entry.Name, err)
			}
			writeErr := ex.file(entry, content)
			closeErr := content.Close()
			if writeErr != nil {
				return writeErr
			}
			if closeErr != nil {
				return fmt.Errorf("archive: close zip entry %s: %w", entry.Name, closeErr)
			}
		}
	}
	return nil
}

// zipEntry 把 zip 文件头转换为统一条目。
func zipEntry(file *zip.File) Entry {
	name := file.Name
	// FileInfo().IsDir() 同时兼容以 / 结尾的目录条目。
	isDir := file.FileInfo().IsDir()
	if isDir {
		name = strings.TrimSuffix(name, "/")
	}
	return Entry{
		Name:    name,
		Size:    int64(file.UncompressedSize64),
		Mode:    file.Mode(),
		ModTime: file.Modified,
		IsDir:   isDir,
	}
}

// readZipSymlink 读取符号链接目标。zip 中链接目标存放在文件内容里。
func readZipSymlink(file *zip.File) (string, error) {
	content, err := file.Open()
	if err != nil {
		return "", fmt.Errorf("archive: open zip symlink %s: %w", file.Name, err)
	}
	defer content.Close()

	// 链接目标不可能很长，限制读取避免异常归档消耗内存。
	data, err := io.ReadAll(io.LimitReader(content, 4096))
	if err != nil {
		return "", fmt.Errorf("archive: read zip symlink %s: %w", file.Name, err)
	}
	return strings.TrimSpace(string(data)), nil
}

// writeZip 写出 zip。
func writeZip(w io.Writer, entries []sourceEntry) error {
	writer := zip.NewWriter(w)
	for _, entry := range entries {
		header, err := zip.FileInfoHeader(entry.info)
		if err != nil {
			_ = writer.Close()
			return fmt.Errorf("archive: zip header %s: %w", entry.name, err)
		}
		header.Name = entry.name
		if entry.info.IsDir() {
			header.Name = strings.TrimSuffix(header.Name, "/") + "/"
			header.Method = zip.Store
		} else {
			header.Method = zip.Deflate
		}

		dst, err := writer.CreateHeader(header)
		if err != nil {
			_ = writer.Close()
			return fmt.Errorf("archive: create zip entry %s: %w", entry.name, err)
		}
		if entry.info.IsDir() {
			continue
		}
		if err := copyFileTo(dst, entry.path); err != nil {
			_ = writer.Close()
			return err
		}
	}
	if err := writer.Close(); err != nil {
		return fmt.Errorf("archive: close zip: %w", err)
	}
	return nil
}

// copyFileTo 把本地文件内容写入目标 writer。
func copyFileTo(w io.Writer, path string) error {
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("archive: open %s: %w", path, err)
	}
	defer file.Close()

	if _, err := io.Copy(w, file); err != nil {
		return fmt.Errorf("archive: copy %s: %w", path, err)
	}
	return nil
}
