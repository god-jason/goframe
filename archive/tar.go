package archive

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"strings"
)

// listTar 读取 tar 条目。
func listTar(r io.Reader) ([]Entry, error) {
	reader := tar.NewReader(r)
	var entries []Entry
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			return entries, nil
		}
		if err != nil {
			return nil, fmt.Errorf("archive: read tar: %w", err)
		}
		if header.Typeflag == tar.TypeXGlobalHeader {
			continue
		}
		entries = append(entries, Entry{
			Name:    header.Name,
			Size:    header.Size,
			Mode:    fs.FileMode(header.Mode),
			ModTime: header.ModTime,
			IsDir:   header.Typeflag == tar.TypeDir,
		})
	}
}

// tar 解压 tar 流。
func (e *extractor) tar(r io.Reader) error {
	reader := tar.NewReader(r)
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("archive: read tar: %w", err)
		}

		entry := Entry{
			Name:    header.Name,
			Size:    header.Size,
			Mode:    fs.FileMode(header.Mode),
			ModTime: header.ModTime,
			IsDir:   header.Typeflag == tar.TypeDir,
		}

		switch header.Typeflag {
		case tar.TypeDir:
			if err := e.dir(entry); err != nil {
				return err
			}
		case tar.TypeReg, tar.TypeRegA:
			if err := e.file(entry, reader); err != nil {
				return err
			}
		case tar.TypeSymlink:
			if err := e.symlink(entry, header.Linkname); err != nil {
				return err
			}
		case tar.TypeLink:
			return fmt.Errorf("%w: hard link %q", ErrUnsafeEntry, header.Name)
		case tar.TypeXGlobalHeader:
			continue
		default:
			// 字符设备、块设备、FIFO 等不允许解压。
			return fmt.Errorf("%w: unsupported tar entry type %d (%q)", ErrUnsafeEntry, header.Typeflag, header.Name)
		}
	}
}

// writeTar 写出 tar，可选 gzip 压缩。
func writeTar(w io.Writer, entries []sourceEntry, gzipped bool) (err error) {
	var gzipWriter *gzip.Writer
	if gzipped {
		gzipWriter = gzip.NewWriter(w)
		w = gzipWriter
	}
	tarWriter := tar.NewWriter(w)

	defer func() {
		closeErr := tarWriter.Close()
		if gzipWriter != nil {
			if gzErr := gzipWriter.Close(); closeErr == nil {
				closeErr = gzErr
			}
		}
		if err == nil && closeErr != nil {
			err = fmt.Errorf("archive: close tar: %w", closeErr)
		}
	}()

	for _, entry := range entries {
		header, headerErr := tar.FileInfoHeader(entry.info, "")
		if headerErr != nil {
			return fmt.Errorf("archive: tar header %s: %w", entry.name, headerErr)
		}
		header.Name = entry.name
		if entry.info.IsDir() {
			header.Name = strings.TrimSuffix(header.Name, "/") + "/"
		}
		if headerErr := tarWriter.WriteHeader(header); headerErr != nil {
			return fmt.Errorf("archive: write tar header %s: %w", entry.name, headerErr)
		}
		if entry.info.IsDir() {
			continue
		}
		if copyErr := copyFileTo(tarWriter, entry.path); copyErr != nil {
			return copyErr
		}
	}
	return nil
}

// writeGzipFile 把单个文件写成 gzip 流。
func writeGzipFile(w io.Writer, path string) (err error) {
	gzipWriter := gzip.NewWriter(w)
	defer func() {
		if closeErr := gzipWriter.Close(); err == nil && closeErr != nil {
			err = fmt.Errorf("archive: close gzip: %w", closeErr)
		}
	}()

	file, openErr := os.Open(path)
	if openErr != nil {
		return fmt.Errorf("archive: open %s: %w", path, openErr)
	}
	defer file.Close()

	if _, copyErr := io.Copy(gzipWriter, file); copyErr != nil {
		return fmt.Errorf("archive: compress %s: %w", path, copyErr)
	}
	return nil
}
