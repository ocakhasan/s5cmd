package e2e

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"gotest.tools/v3/assert"
	"gotest.tools/v3/fs"
	"gotest.tools/v3/icmd"
)

// sync folder/ folder2/
func TestSyncLocalToLocalNotPermitted(t *testing.T) {
	t.Parallel()

	_, s5cmd, cleanup := setup(t)
	defer cleanup()

	const (
		sourceFolder = "source"
		destFolder   = "dest"
	)
	sourceWorkDir := fs.NewDir(t, sourceFolder)
	destWorkDir := fs.NewDir(t, destFolder)

	srcpath := filepath.ToSlash(sourceWorkDir.Path())
	destpath := filepath.ToSlash(destWorkDir.Path())

	cmd := s5cmd("sync", srcpath, destpath)
	result := icmd.RunCmd(cmd)

	result.Assert(t, icmd.Expected{ExitCode: 1})

	assertLines(t, result.Stderr(), map[int]compareFunc{
		0: equals(`ERROR "sync %s %s": local->local sync operations are not permitted`, srcpath, destpath),
	})
}

// sync source.go s3://bucket
func TestSyncLocalFileToS3NotPermitted(t *testing.T) {
	t.Parallel()

	s3client, s5cmd, cleanup := setup(t)
	defer cleanup()

	const (
		filename = "source.go"
		bucket   = "bucket"
	)

	createBucket(t, s3client, bucket)

	sourceWorkDir := fs.NewFile(t, filename)
	srcpath := filepath.ToSlash(sourceWorkDir.Path())
	dstpath := fmt.Sprintf("s3://%s/", bucket)

	cmd := s5cmd("sync", srcpath, dstpath)
	result := icmd.RunCmd(cmd)

	result.Assert(t, icmd.Expected{ExitCode: 1})

	assertLines(t, result.Stderr(), map[int]compareFunc{
		0: equals(`ERROR "sync %s %s": local source must be a directory`, srcpath, dstpath),
	})
}

// sync s3://bucket/source.go .
func TestSyncSingleS3ObjectToLocalNotPermitted(t *testing.T) {
	t.Parallel()

	s3client, s5cmd, cleanup := setup(t)
	defer cleanup()

	const (
		filename = "source.go"
		bucket   = "bucket"
		content  = "content"
	)

	createBucket(t, s3client, bucket)
	putFile(t, s3client, bucket, filename, content)

	srcpath := fmt.Sprintf("s3://%s/%s", bucket, filename)

	cmd := s5cmd("sync", srcpath, ".")
	result := icmd.RunCmd(cmd)
	result.Assert(t, icmd.Expected{ExitCode: 1})

	assertLines(t, result.Stderr(), map[int]compareFunc{
		0: equals(`ERROR "sync %s .": remote source %q must be a bucket or a prefix`, srcpath, srcpath),
	})
}

// sync folder/ s3://bucket
func TestSyncLocalFolderToS3EmptyBucket(t *testing.T) {
	t.Parallel()
	s3client, s5cmd, cleanup := setup(t)
	defer cleanup()

	bucket := s3BucketFromTestName(t)
	createBucket(t, s3client, bucket)

	folderLayout := []fs.PathOp{
		fs.WithFile("testfile1.txt", "this is a test file 1"),
		fs.WithFile("readme.md", "this is a readme file"),
		fs.WithDir("a",
			fs.WithFile("another_test_file.txt", "yet another txt file. yatf."),
		),
		fs.WithDir("b",
			fs.WithFile("filename-with-hypen.gz", "file has hypen in its name"),
		),
	}

	workdir := fs.NewDir(t, "somedir", folderLayout...)
	defer workdir.Remove()

	src := fmt.Sprintf("%v/", workdir.Path())
	src = filepath.ToSlash(src)
	dst := fmt.Sprintf("s3://%v/", bucket)

	cmd := s5cmd("sync", src, dst)
	result := icmd.RunCmd(cmd)

	result.Assert(t, icmd.Success)

	assertLines(t, result.Stdout(), map[int]compareFunc{
		0: equals(`cp %va/another_test_file.txt %va/another_test_file.txt`, src, dst),
		1: equals(`cp %vb/filename-with-hypen.gz %vb/filename-with-hypen.gz`, src, dst),
		2: equals(`cp %vreadme.md %vreadme.md`, src, dst),
		3: equals(`cp %vtestfile1.txt %vtestfile1.txt`, src, dst),
	}, sortInput(true))

	// assert local filesystem
	expected := fs.Expected(t, folderLayout...)
	assert.Assert(t, fs.Equal(workdir.Path(), expected))

	expectedS3Content := map[string]string{
		"testfile1.txt":            "this is a test file 1",
		"readme.md":                "this is a readme file",
		"b/filename-with-hypen.gz": "file has hypen in its name",
		"a/another_test_file.txt":  "yet another txt file. yatf.",
	}

	// assert s3
	for key, content := range expectedS3Content {
		assert.Assert(t, ensureS3Object(s3client, bucket, key, content))
	}
}

// sync  s3://bucket/* folder/
func TestSyncS3BucketToEmptyFolder(t *testing.T) {
	t.Parallel()
	s3client, s5cmd, cleanup := setup(t)
	defer cleanup()

	bucket := s3BucketFromTestName(t)
	createBucket(t, s3client, bucket)

	S3Content := map[string]string{
		"testfile1.txt":           "this is a test file 1",
		"readme.md":               "this is a readme file",
		"a/another_test_file.txt": "yet another txt file. yatf.",
		"abc/def/test.py":         "file in nested folders",
	}

	for filename, content := range S3Content {
		putFile(t, s3client, bucket, filename, content)
	}

	workdir := fs.NewDir(t, "somedir")
	defer workdir.Remove()

	bucketPath := fmt.Sprintf("s3://%v", bucket)
	src := fmt.Sprintf("%v/*", bucketPath)
	dst := fmt.Sprintf("%v/", workdir.Path())
	dst = filepath.ToSlash(dst)

	cmd := s5cmd("sync", src, dst)
	result := icmd.RunCmd(cmd)

	result.Assert(t, icmd.Success)

	assertLines(t, result.Stdout(), map[int]compareFunc{
		0: equals(`cp %v/a/another_test_file.txt %va/another_test_file.txt`, bucketPath, dst),
		1: equals(`cp %v/abc/def/test.py %vabc/def/test.py`, bucketPath, dst),
		2: equals(`cp %v/readme.md %vreadme.md`, bucketPath, dst),
		3: equals(`cp %v/testfile1.txt %vtestfile1.txt`, bucketPath, dst),
	}, sortInput(true))

	expectedFolderLayout := []fs.PathOp{
		fs.WithFile("testfile1.txt", "this is a test file 1"),
		fs.WithFile("readme.md", "this is a readme file"),
		fs.WithDir("a",
			fs.WithFile("another_test_file.txt", "yet another txt file. yatf."),
		),
		fs.WithDir("abc",
			fs.WithDir("def",
				fs.WithFile("test.py", "file in nested folders"),
			),
		),
	}

	// assert local filesystem
	expected := fs.Expected(t, expectedFolderLayout...)
	assert.Assert(t, fs.Equal(workdir.Path(), expected))

	// assert s3
	for key, content := range S3Content {
		assert.Assert(t, ensureS3Object(s3client, bucket, key, content))
	}
}

// sync  s3://bucket/* s3://destbucket/prefix/
func TestSyncS3BucketToEmptyS3Bucket(t *testing.T) {
	t.Parallel()
	s3client, s5cmd, cleanup := setup(t)
	defer cleanup()

	bucket := s3BucketFromTestName(t)
	const (
		destbucket = "destbucket"
		prefix     = "prefix"
	)
	createBucket(t, s3client, bucket)
	createBucket(t, s3client, destbucket)

	S3Content := map[string]string{
		"testfile1.txt":           "this is a test file 1",
		"readme.md":               "this is a readme file",
		"a/another_test_file.txt": "yet another txt file. yatf.",
		"abc/def/test.py":         "file in nested folders",
	}

	for filename, content := range S3Content {
		putFile(t, s3client, bucket, filename, content)
	}

	bucketPath := fmt.Sprintf("s3://%v", bucket)
	src := fmt.Sprintf("%v/*", bucketPath)
	dst := fmt.Sprintf("s3://%v/%v/", destbucket, prefix)

	cmd := s5cmd("sync", src, dst)
	result := icmd.RunCmd(cmd)

	result.Assert(t, icmd.Success)

	assertLines(t, result.Stdout(), map[int]compareFunc{
		0: equals(`cp %v/a/another_test_file.txt %va/another_test_file.txt`, bucketPath, dst),
		1: equals(`cp %v/abc/def/test.py %vabc/def/test.py`, bucketPath, dst),
		2: equals(`cp %v/readme.md %vreadme.md`, bucketPath, dst),
		3: equals(`cp %v/testfile1.txt %vtestfile1.txt`, bucketPath, dst),
	}, sortInput(true))

	// assert  s3 objects in source bucket.
	for key, content := range S3Content {
		assert.Assert(t, ensureS3Object(s3client, bucket, key, content))
	}

	// assert s3 objects in dest bucket
	for key, content := range S3Content {
		key = fmt.Sprintf("/%s/%s", prefix, key) // add the prefix
		assert.Assert(t, ensureS3Object(s3client, destbucket, key, content))
	}
}

// sync folder/ s3://bucket (source older, same objects)
func TestSyncLocalFolderToS3BucketSameObjectsSourceOlder(t *testing.T) {
	t.Parallel()
	s3client, s5cmd, cleanup := setup(t)
	defer cleanup()

	bucket := s3BucketFromTestName(t)
	createBucket(t, s3client, bucket)

	now := time.Now().UTC()
	timestamp := fs.WithTimestamps(
		now.Add(-time.Minute), // access time
		now.Add(-time.Minute), // mod time
	)

	folderLayout := []fs.PathOp{
		fs.WithFile("main.py", "this is a python file", timestamp),
		fs.WithFile("testfile1.txt", "this is a test file 1", timestamp),
		fs.WithFile("readme.md", "this is a readme file", timestamp),
		fs.WithDir("a",
			fs.WithFile("another_test_file.txt", "yet another txt file. yatf.", timestamp),
			timestamp,
		),
	}

	// for expected local structure, same as folderLayout without timestamps.
	folderLayoutWithoutTimestamp := []fs.PathOp{
		fs.WithFile("main.py", "this is a python file"),
		fs.WithFile("testfile1.txt", "this is a test file 1"),
		fs.WithFile("readme.md", "this is a readme file"),
		fs.WithDir("a",
			fs.WithFile("another_test_file.txt", "yet another txt file. yatf."),
		),
	}

	workdir := fs.NewDir(t, "somedir", folderLayout...)
	defer workdir.Remove()

	S3Content := map[string]string{
		"main.py":                 "this is a python file",
		"testfile1.txt":           "this is a test file 1",
		"readme.md":               "this is a readme file",
		"a/another_test_file.txt": "yet another txt file. yatf.",
	}

	for filename, content := range S3Content {
		putFile(t, s3client, bucket, filename, content)
	}

	src := fmt.Sprintf("%v/", workdir.Path())
	src = filepath.ToSlash(src)
	dst := fmt.Sprintf("s3://%v/", bucket)

	// log debug
	cmd := s5cmd("--log", "debug", "sync", src, dst)
	result := icmd.RunCmd(cmd)

	result.Assert(t, icmd.Success)

	assertLines(t, result.Stdout(), map[int]compareFunc{
		0: equals(`DEBUG "sync %va/another_test_file.txt %va/another_test_file.txt": object is newer or same age`, src, dst),
		1: equals(`DEBUG "sync %vmain.py %vmain.py": object is newer or same age`, src, dst),
		2: equals(`DEBUG "sync %vreadme.md %vreadme.md": object is newer or same age`, src, dst),
		3: equals(`DEBUG "sync %vtestfile1.txt %vtestfile1.txt": object is newer or same age`, src, dst),
	}, sortInput(true))

	// expected folder structure without the timestamp.
	expected := fs.Expected(t, folderLayoutWithoutTimestamp...)
	assert.Assert(t, fs.Equal(workdir.Path(), expected))

	// assert s3
	for key, content := range S3Content {
		assert.Assert(t, ensureS3Object(s3client, bucket, key, content))
	}
}

// sync folder/ s3://bucket (source newer)
func TestSyncLocalFolderToS3BucketSameObjectsSourceNewer(t *testing.T) {
	t.Parallel()
	s3client, s5cmd, cleanup := setup(t)
	defer cleanup()

	bucket := s3BucketFromTestName(t)
	createBucket(t, s3client, bucket)

	now := time.Now().UTC()
	timestamp := fs.WithTimestamps(
		now.Add(time.Minute), // access time
		now.Add(time.Minute), // mod time
	)

	folderLayout := []fs.PathOp{
		fs.WithFile("testfile1.txt", "this is a test file 2", timestamp), // content different from s3
		fs.WithFile("readme.md", "this is a readve file", timestamp),     // content different from s3
		fs.WithDir("dir",
			fs.WithFile("main.py", "python file 2", timestamp), // content different from s3
			timestamp,
		),
	}

	// for expected local structure, same as folderLayout without timestamps.
	folderLayoutWithoutTimestamp := []fs.PathOp{
		fs.WithFile("testfile1.txt", "this is a test file 2"), // content different from s3
		fs.WithFile("readme.md", "this is a readve file"),     // content different from s3
		fs.WithDir("dir",
			fs.WithFile("main.py", "python file 2"), // content different from s3
		),
	}

	workdir := fs.NewDir(t, "somedir", folderLayout...)
	defer workdir.Remove()

	S3Content := map[string]string{
		"testfile1.txt": "this is a test file 1",
		"readme.md":     "this is a readme file",
		"dir/main.py":   "python file 1",
	}

	for filename, content := range S3Content {
		putFile(t, s3client, bucket, filename, content)
	}

	src := fmt.Sprintf("%v/", workdir.Path())
	src = filepath.ToSlash(src)
	dst := fmt.Sprintf("s3://%v/", bucket)

	// log debug
	cmd := s5cmd("--log", "debug", "sync", src, dst)
	result := icmd.RunCmd(cmd)

	result.Assert(t, icmd.Success)

	assertLines(t, result.Stdout(), map[int]compareFunc{
		0: equals(`cp %vdir/main.py %vdir/main.py`, src, dst),
		1: equals(`cp %vreadme.md %vreadme.md`, src, dst),
		2: equals(`cp %vtestfile1.txt %vtestfile1.txt`, src, dst),
	}, sortInput(true))

	// expected folder structure without the timestamp.
	expected := fs.Expected(t, folderLayoutWithoutTimestamp...)
	assert.Assert(t, fs.Equal(workdir.Path(), expected))

	expectedS3Content := map[string]string{
		"testfile1.txt": "this is a test file 2", // same as local source
		"readme.md":     "this is a readve file", // same as local source
		"dir/main.py":   "python file 2",         // same as local source
	}

	// assert s3
	for key, content := range expectedS3Content {
		assert.Assert(t, ensureS3Object(s3client, bucket, key, content))
	}
}

// sync s3://bucket/* folder/ (source older, same objects)
func TestSyncS3BucketToLocalFolderSameObjectsSourceOlder(t *testing.T) {
	t.Parallel()
	s3client, s5cmd, cleanup := setup(t)
	defer cleanup()

	bucket := s3BucketFromTestName(t)
	createBucket(t, s3client, bucket)

	// timestamp for local folder, local ise newer.
	now := time.Now().UTC()
	timestamp := fs.WithTimestamps(
		now.Add(time.Minute), // access time
		now.Add(time.Minute), // mod time
	)

	folderLayout := []fs.PathOp{
		fs.WithFile("main.py", "this is a python file", timestamp),
		fs.WithFile("testfile1.txt", "this is a test file 1", timestamp),
		fs.WithFile("readme.md", "this is a readme file", timestamp),
		fs.WithDir("a",
			fs.WithFile("another_test_file.txt", "yet another txt file. yatf.", timestamp),
			timestamp,
		),
	}

	// for expected local structure, same as folderLayout without timestamps.
	// content should not be changed.
	expectedLayout := []fs.PathOp{
		fs.WithFile("main.py", "this is a python file"),
		fs.WithFile("testfile1.txt", "this is a test file 1"),
		fs.WithFile("readme.md", "this is a readme file"),
		fs.WithDir("a",
			fs.WithFile("another_test_file.txt", "yet another txt file. yatf."),
		),
	}

	workdir := fs.NewDir(t, "somedir", folderLayout...)
	defer workdir.Remove()

	S3Content := map[string]string{
		"main.py":                 "this is a python file",
		"testfile1.txt":           "this is a test file 2",       // content different from local
		"readme.md":               "this is a readme file",       // content different from local
		"a/another_test_file.txt": "yet another txt file. yatg.", // content different from local
	}

	for filename, content := range S3Content {
		putFile(t, s3client, bucket, filename, content)
	}

	bucketPath := fmt.Sprintf("s3://%v", bucket)
	src := fmt.Sprintf("%s/*", bucketPath)
	dst := fmt.Sprintf("%v/", workdir.Path())
	dst = filepath.ToSlash(dst)

	// log debug
	cmd := s5cmd("--log", "debug", "sync", src, dst)
	result := icmd.RunCmd(cmd)

	result.Assert(t, icmd.Success)

	assertLines(t, result.Stdout(), map[int]compareFunc{
		0: equals(`DEBUG "sync %v/a/another_test_file.txt %va/another_test_file.txt": object is newer or same age`, bucketPath, dst),
		1: equals(`DEBUG "sync %v/main.py %vmain.py": object is newer or same age`, bucketPath, dst),
		2: equals(`DEBUG "sync %v/readme.md %vreadme.md": object is newer or same age`, bucketPath, dst),
		3: equals(`DEBUG "sync %v/testfile1.txt %vtestfile1.txt": object is newer or same age`, bucketPath, dst),
	}, sortInput(true))

	// expected folder structure without the timestamp.
	expected := fs.Expected(t, expectedLayout...)
	assert.Assert(t, fs.Equal(workdir.Path(), expected))

	// assert s3
	for key, content := range S3Content {
		assert.Assert(t, ensureS3Object(s3client, bucket, key, content))
	}
}

// sync s3://bucket/* folder/ (source newer, same objects)
func TestSyncS3BucketToLocalFolderSameObjectsSourceNewer(t *testing.T) {
	t.Parallel()
	s3client, s5cmd, cleanup := setup(t)
	defer cleanup()

	bucket := s3BucketFromTestName(t)
	createBucket(t, s3client, bucket)

	// timestamp for local folder, source ise newer.
	now := time.Now().UTC()
	timestamp := fs.WithTimestamps(
		now.Add(-time.Minute), // access time
		now.Add(-time.Minute), // mod time
	)

	folderLayout := []fs.PathOp{
		fs.WithFile("main.py", "this is a python file", timestamp),
		fs.WithFile("testfile1.txt", "this is a test file 1", timestamp),
		fs.WithFile("readme.md", "this is a readme file", timestamp),
		fs.WithDir("a",
			fs.WithFile("another_test_file.txt", "yet another txt file. yatf.", timestamp),
			timestamp,
		),
	}

	// for expected local structure, same as folderLayout without timestamps.
	// content should be same as s3 contents.
	expectedLayout := []fs.PathOp{
		fs.WithFile("main.py", "this is a python file"),
		fs.WithFile("testfile1.txt", "this is a test file 2"),
		fs.WithFile("readme.md", "this is a readve file"),
		fs.WithDir("a",
			fs.WithFile("another_test_file.txt", "yet another txt file. yatg:"),
		),
	}

	workdir := fs.NewDir(t, "somedir", folderLayout...)
	defer workdir.Remove()

	S3Content := map[string]string{
		"main.py":                 "this is a python file",
		"testfile1.txt":           "this is a test file 2",       // content different from local
		"readme.md":               "this is a readve file",       // content different from local
		"a/another_test_file.txt": "yet another txt file. yatg:", // content different from local
	}

	for filename, content := range S3Content {
		putFile(t, s3client, bucket, filename, content)
	}

	bucketPath := fmt.Sprintf("s3://%v", bucket)
	src := fmt.Sprintf("%s/*", bucketPath)
	dst := fmt.Sprintf("%v/", workdir.Path())
	dst = filepath.ToSlash(dst)

	// log debug
	cmd := s5cmd("--log", "debug", "sync", src, dst)
	result := icmd.RunCmd(cmd)

	result.Assert(t, icmd.Success)

	assertLines(t, result.Stdout(), map[int]compareFunc{
		0: equals(`cp %v/a/another_test_file.txt %va/another_test_file.txt`, bucketPath, dst),
		1: equals(`cp %v/main.py %vmain.py`, bucketPath, dst),
		2: equals(`cp %v/readme.md %vreadme.md`, bucketPath, dst),
		3: equals(`cp %v/testfile1.txt %vtestfile1.txt`, bucketPath, dst),
	}, sortInput(true))

	// expected folder structure without the timestamp.
	expected := fs.Expected(t, expectedLayout...)
	assert.Assert(t, fs.Equal(workdir.Path(), expected))

	// assert s3
	for key, content := range S3Content {
		assert.Assert(t, ensureS3Object(s3client, bucket, key, content))
	}
}

// sync s3://bucket/* s3://destbucket/ (source newer, same objects, different content, same sizes)
func TestSyncS3BucketToS3BucketSameSizesSourceNewer(t *testing.T) {
	t.Parallel()
	s3client, s5cmd, cleanup := setup(t)
	defer cleanup()

	bucket := s3BucketFromTestName(t)
	destbucket := "destbucket"

	createBucket(t, s3client, bucket)
	createBucket(t, s3client, destbucket)

	sourceS3Content := map[string]string{
		"main.py":                 "this is a python file",
		"testfile1.txt":           "this is a test file 2",
		"readme.md":               "this is a readve file",
		"a/another_test_file.txt": "yet another txt file. yatg:",
	}

	// the file sizes are same, content different.
	destS3Content := map[string]string{
		"main.py":                 "this is a python abcd",
		"testfile1.txt":           "this is a test abcd 2",
		"readme.md":               "this is a readve abcd",
		"a/another_test_file.txt": "yet another txt abcd. yatg:",
	}

	// first put destination to assure destination is older.
	for filename, content := range destS3Content {
		putFile(t, s3client, destbucket, filename, content)
	}

	for filename, content := range sourceS3Content {
		putFile(t, s3client, bucket, filename, content)
	}

	bucketPath := fmt.Sprintf("s3://%v", bucket)
	src := fmt.Sprintf("%s/*", bucketPath)
	dst := fmt.Sprintf("s3://%v/", destbucket)

	// log debug
	cmd := s5cmd("--log", "debug", "sync", src, dst)
	result := icmd.RunCmd(cmd)

	result.Assert(t, icmd.Success)

	assertLines(t, result.Stdout(), map[int]compareFunc{
		0: equals(`cp %v/a/another_test_file.txt %va/another_test_file.txt`, bucketPath, dst),
		1: equals(`cp %v/main.py %vmain.py`, bucketPath, dst),
		2: equals(`cp %v/readme.md %vreadme.md`, bucketPath, dst),
		3: equals(`cp %v/testfile1.txt %vtestfile1.txt`, bucketPath, dst),
	}, sortInput(true))

	// assert s3 objects in source
	for key, content := range sourceS3Content {
		assert.Assert(t, ensureS3Object(s3client, bucket, key, content))
	}

	// assert s3 objects in destination (should be same as source)
	for key, content := range sourceS3Content {
		assert.Assert(t, ensureS3Object(s3client, destbucket, key, content))
	}
}

// sync s3://bucket/* s3://destbucket/ (source older, same objects, different content, same sizes)
func TestSyncS3BucketToS3BucketSameSizesSourceOlder(t *testing.T) {
	t.Parallel()
	s3client, s5cmd, cleanup := setup(t)
	defer cleanup()

	bucket := s3BucketFromTestName(t)
	destbucket := "destbucket"

	createBucket(t, s3client, bucket)
	createBucket(t, s3client, destbucket)

	sourceS3Content := map[string]string{
		"main.py":                 "this is a python file",
		"testfile1.txt":           "this is a test file 2",
		"readme.md":               "this is a readve file",
		"a/another_test_file.txt": "yet another txt file. yatg:",
	}

	// the file sizes are same, content different.
	destS3Content := map[string]string{
		"main.py":                 "this is a python abcd",
		"testfile1.txt":           "this is a test abcd 2",
		"readme.md":               "this is a readve abcd",
		"a/another_test_file.txt": "yet another txt abcd. yatg:",
	}

	// first put source to assure source is older.
	for filename, content := range sourceS3Content {
		putFile(t, s3client, bucket, filename, content)
	}

	for filename, content := range destS3Content {
		putFile(t, s3client, destbucket, filename, content)
	}

	bucketPath := fmt.Sprintf("s3://%v", bucket)
	src := fmt.Sprintf("%s/*", bucketPath)
	dst := fmt.Sprintf("s3://%v/", destbucket)

	// log debug
	cmd := s5cmd("--log", "debug", "sync", src, dst)
	result := icmd.RunCmd(cmd)

	result.Assert(t, icmd.Success)

	assertLines(t, result.Stdout(), map[int]compareFunc{
		0: equals(`DEBUG "sync %v/a/another_test_file.txt %va/another_test_file.txt": object is newer or same age`, bucketPath, dst),
		1: equals(`DEBUG "sync %v/main.py %vmain.py": object is newer or same age`, bucketPath, dst),
		2: equals(`DEBUG "sync %v/readme.md %vreadme.md": object is newer or same age`, bucketPath, dst),
		3: equals(`DEBUG "sync %v/testfile1.txt %vtestfile1.txt": object is newer or same age`, bucketPath, dst),
	}, sortInput(true))

	// assert s3 objects in source
	for key, content := range sourceS3Content {
		assert.Assert(t, ensureS3Object(s3client, bucket, key, content))
	}

	// assert s3 objects in destination (should not change).
	for key, content := range destS3Content {
		assert.Assert(t, ensureS3Object(s3client, destbucket, key, content))
	}
}

// sync --size-only s3://bucket/* folder/
func TestSyncS3BucketToLocalFolderSameObjectsSizeOnly(t *testing.T) {
	t.Parallel()
	s3client, s5cmd, cleanup := setup(t)
	defer cleanup()

	bucket := s3BucketFromTestName(t)
	createBucket(t, s3client, bucket)

	folderLayout := []fs.PathOp{
		fs.WithFile("test.py", "this is a python file"),
		fs.WithFile("testfile1.txt", "this is a test file 1"),
		fs.WithFile("readme.md", "this is a readme file"),
		fs.WithDir("a",
			fs.WithFile("another_test_file.txt", "yet another txt file. yatf."),
		),
	}

	workdir := fs.NewDir(t, "somedir", folderLayout...)
	defer workdir.Remove()

	S3Content := map[string]string{
		"test.py":                 "this is a python file with some extension", // content different from local, different size
		"testfile1.txt":           "this is a test file 2",                     // content different from local, same size
		"readme.md":               "this is a readve file",                     // content different from local, same size
		"a/another_test_file.txt": "yet another txt file. yatg.",               // content different from local, same size
		"abc/def/main.py":         "python file",                               // local does not have it.
	}

	for filename, content := range S3Content {
		putFile(t, s3client, bucket, filename, content)
	}

	bucketPath := fmt.Sprintf("s3://%v", bucket)
	src := fmt.Sprintf("%s/*", bucketPath)
	dst := fmt.Sprintf("%v/", workdir.Path())
	dst = filepath.ToSlash(dst)

	// log debug
	cmd := s5cmd("--log", "debug", "sync", "--size-only", src, dst)
	result := icmd.RunCmd(cmd)

	result.Assert(t, icmd.Success)

	assertLines(t, result.Stdout(), map[int]compareFunc{
		0: equals(`DEBUG "sync %v/a/another_test_file.txt %va/another_test_file.txt": object size matches`, bucketPath, dst),
		1: equals(`DEBUG "sync %v/readme.md %vreadme.md": object size matches`, bucketPath, dst),
		2: equals(`DEBUG "sync %v/testfile1.txt %vtestfile1.txt": object size matches`, bucketPath, dst),
		3: equals(`cp %v/abc/def/main.py %vabc/def/main.py`, bucketPath, dst),
		4: equals(`cp %v/test.py %vtest.py`, bucketPath, dst),
	}, sortInput(true))

	expectedFolderLayout := []fs.PathOp{
		fs.WithFile("test.py", "this is a python file with some extension"),
		fs.WithFile("testfile1.txt", "this is a test file 1"),
		fs.WithFile("readme.md", "this is a readme file"),
		fs.WithDir("a",
			fs.WithFile("another_test_file.txt", "yet another txt file. yatf."),
		),
		fs.WithDir("abc",
			fs.WithDir("def",
				fs.WithFile("main.py", "python file"),
			),
		),
	}

	// expected folder structure without the timestamp.
	expected := fs.Expected(t, expectedFolderLayout...)
	assert.Assert(t, fs.Equal(workdir.Path(), expected))

	// assert s3
	for key, content := range S3Content {
		assert.Assert(t, ensureS3Object(s3client, bucket, key, content))
	}
}

// sync --size-only folder/ s3://bucket/
func TestSyncLocalFolderToS3BucketSameObjectsSizeOnly(t *testing.T) {
	t.Parallel()
	s3client, s5cmd, cleanup := setup(t)
	defer cleanup()

	bucket := s3BucketFromTestName(t)
	createBucket(t, s3client, bucket)

	folderLayout := []fs.PathOp{
		fs.WithFile("test.py", "this is a python file"),       // remote has it, different content, size same
		fs.WithFile("testfile1.txt", "this is a test file 1"), // remote has it, size different.
		fs.WithFile("readme.md", "this is a readme file"),     // remote has it, same object.
		fs.WithDir("a",
			fs.WithFile("another_test_file.txt", "yet another txt file. yatf."), // remote has it, different content, same size.
		),
		fs.WithDir("abc",
			fs.WithDir("def",
				fs.WithFile("main.py", "python file"), // remote does not have it
			),
		),
	}

	workdir := fs.NewDir(t, "somedir", folderLayout...)
	defer workdir.Remove()

	S3Content := map[string]string{
		"test.py":                 "this is a python abcd",
		"testfile1.txt":           "this is a test file 100",
		"readme.md":               "this is a readme file",
		"a/another_test_file.txt": "yet another txt file. yatg.",
	}

	for filename, content := range S3Content {
		putFile(t, s3client, bucket, filename, content)
	}

	src := fmt.Sprintf("%v/", workdir.Path())
	src = filepath.ToSlash(src)
	dst := fmt.Sprintf("s3://%s/", bucket)

	// log debug
	cmd := s5cmd("--log", "debug", "sync", "--size-only", src, dst)
	result := icmd.RunCmd(cmd)

	result.Assert(t, icmd.Success)

	assertLines(t, result.Stdout(), map[int]compareFunc{
		0: equals(`DEBUG "sync %va/another_test_file.txt %va/another_test_file.txt": object size matches`, src, dst),
		1: equals(`DEBUG "sync %vreadme.md %vreadme.md": object size matches`, src, dst),
		2: equals(`DEBUG "sync %vtest.py %vtest.py": object size matches`, src, dst),
		3: equals(`cp %vabc/def/main.py %vabc/def/main.py`, src, dst),
		4: equals(`cp %vtestfile1.txt %vtestfile1.txt`, src, dst),
	}, sortInput(true))

	// expected folder structure without the timestamp.
	expected := fs.Expected(t, folderLayout...)
	assert.Assert(t, fs.Equal(workdir.Path(), expected))

	expectedS3Content := map[string]string{
		"test.py":                 "this is a python abcd",
		"testfile1.txt":           "this is a test file 1",
		"readme.md":               "this is a readme file",
		"a/another_test_file.txt": "yet another txt file. yatg.",
		"abc/def/main.py":         "python file",
	}

	// assert s3
	for key, content := range expectedS3Content {
		assert.Assert(t, ensureS3Object(s3client, bucket, key, content))
	}
}

// sync --size-only s3://bucket/* s3://destbucket/
func TestSyncS3BucketToS3BucketSizeOnly(t *testing.T) {
	t.Parallel()
	s3client, s5cmd, cleanup := setup(t)
	defer cleanup()

	bucket := s3BucketFromTestName(t)
	destbucket := "destbucket"
	createBucket(t, s3client, bucket)
	createBucket(t, s3client, destbucket)

	sourceS3Content := map[string]string{
		"main.py":                 "this is a python file with some extension",
		"testfile1.txt":           "this is a test file 2",
		"readme.md":               "this is a readve file",
		"a/another_test_file.txt": "yet another txt file. yatg:",
	}

	// the file sizes are same, content different.
	// ensure that dest bucket objects is not changed.
	destS3Content := map[string]string{
		"main.py":                 "this is a python abcd", // file size is shorter.
		"testfile1.txt":           "this is a test abcd 2",
		"readme.md":               "this is a readve abcd",
		"a/another_test_file.txt": "yet another txt abcd. yatg:",
	}

	// first put the dest content to make destination older.
	// so in normal case even if the file sizes are same, since
	// source is newer, it should copy all of the objects.
	// but in this case, it will copy only files who has different sizes.
	for filename, content := range destS3Content {
		putFile(t, s3client, destbucket, filename, content)
	}

	for filename, content := range sourceS3Content {
		putFile(t, s3client, bucket, filename, content)
	}

	bucketPath := fmt.Sprintf("s3://%v", bucket)
	src := fmt.Sprintf("%s/*", bucketPath)
	dst := fmt.Sprintf("s3://%v/", destbucket)

	// log debug
	cmd := s5cmd("--log", "debug", "sync", "--size-only", src, dst)
	result := icmd.RunCmd(cmd)

	result.Assert(t, icmd.Success)

	assertLines(t, result.Stdout(), map[int]compareFunc{
		0: equals(`DEBUG "sync %v/a/another_test_file.txt %va/another_test_file.txt": object size matches`, bucketPath, dst),
		1: equals(`DEBUG "sync %v/readme.md %vreadme.md": object size matches`, bucketPath, dst),
		2: equals(`DEBUG "sync %v/testfile1.txt %vtestfile1.txt": object size matches`, bucketPath, dst),
		3: equals(`cp %v/main.py %vmain.py`, bucketPath, dst),
	}, sortInput(true))

	// assert s3 objects in source
	for key, content := range sourceS3Content {
		assert.Assert(t, ensureS3Object(s3client, bucket, key, content))
	}

	expectedDestS3Content := map[string]string{
		"main.py":                 "this is a python file with some extension", // same as source.
		"testfile1.txt":           "this is a test abcd 2",
		"readme.md":               "this is a readve abcd",
		"a/another_test_file.txt": "yet another txt abcd. yatg:",
	}

	// assert s3 objects in destination
	for key, content := range expectedDestS3Content {
		assert.Assert(t, ensureS3Object(s3client, destbucket, key, content))
	}
}

// sync --delete s3://bucket/* .
func TestSyncS3BucketToLocalWithDelete(t *testing.T) {
	t.Parallel()
	s3client, s5cmd, cleanup := setup(t)
	defer cleanup()

	bucket := s3BucketFromTestName(t)
	createBucket(t, s3client, bucket)

	S3Content := map[string]string{
		"contributing.md": "this is a readme file",
	}

	for filename, content := range S3Content {
		putFile(t, s3client, bucket, filename, content)
	}

	folderLayout := []fs.PathOp{
		fs.WithFile("testfile1.txt", "this is a test file 1"),
		fs.WithFile("readme.md", "this is a readme file"),
		fs.WithDir("dir",
			fs.WithFile("main.py", "python file"),
		),
	}

	workdir := fs.NewDir(t, "somedir", folderLayout...)
	defer workdir.Remove()

	src := fmt.Sprintf("s3://%v/", bucket)
	dst := fmt.Sprintf("%v/", workdir.Path())
	dst = filepath.ToSlash(dst)

	cmd := s5cmd("sync", "--delete", src+"*", dst)
	result := icmd.RunCmd(cmd)

	result.Assert(t, icmd.Success)

	assertLines(t, result.Stdout(), map[int]compareFunc{
		0: equals(`cp %vcontributing.md %vcontributing.md`, src, dst),
		1: equals(`rm %vdir/main.py`, dst),
		2: equals(`rm %vreadme.md`, dst),
		3: equals(`rm %vtestfile1.txt`, dst),
	}, sortInput(true))

	expectedFolderLayout := []fs.PathOp{
		fs.WithDir("dir"),
		fs.WithFile("contributing.md", "this is a readme file"),
	}

	// assert local filesystem
	expected := fs.Expected(t, expectedFolderLayout...)
	assert.Assert(t, fs.Equal(workdir.Path(), expected))

	// assert s3
	for key, content := range S3Content {
		assert.Assert(t, ensureS3Object(s3client, bucket, key, content))
	}
}

// sync --delete s3://bucket/* .
func TestSyncLocalToS3BucketWithDelete(t *testing.T) {
	t.Parallel()
	s3client, s5cmd, cleanup := setup(t)
	defer cleanup()

	bucket := s3BucketFromTestName(t)
	createBucket(t, s3client, bucket)

	// first create the source to ensure source is older.
	folderLayout := []fs.PathOp{
		fs.WithFile("contributing.md", "this is a readme file"),
	}

	workdir := fs.NewDir(t, "somedir", folderLayout...)
	defer workdir.Remove()

	S3Content := map[string]string{
		"readme.md":     "this is a readme file",
		"dir/main.py":   "this is a python file",
		"testfile1.txt": "this is a test file 1",
	}

	for filename, content := range S3Content {
		putFile(t, s3client, bucket, filename, content)
	}

	src := fmt.Sprintf("%v/", workdir.Path())
	src = filepath.ToSlash(src)
	dst := fmt.Sprintf("s3://%v/", bucket)

	cmd := s5cmd("sync", "--delete", src, dst)
	result := icmd.RunCmd(cmd)

	result.Assert(t, icmd.Success)

	assertLines(t, result.Stdout(), map[int]compareFunc{
		0: equals(`cp %vcontributing.md %vcontributing.md`, src, dst),
		1: equals(`rm %vdir/main.py`, dst),
		2: equals(`rm %vreadme.md`, dst),
		3: equals(`rm %vtestfile1.txt`, dst),
	}, sortInput(true))

	expectedFolderLayout := []fs.PathOp{
		fs.WithFile("contributing.md", "this is a readme file"),
	}

	// assert local filesystem
	expected := fs.Expected(t, expectedFolderLayout...)
	assert.Assert(t, fs.Equal(workdir.Path(), expected))

	expectedS3Content := map[string]string{
		"contributing.md": "this is a readme file",
	}

	// assert s3 objects
	for key, content := range expectedS3Content {
		assert.Assert(t, ensureS3Object(s3client, bucket, key, content))
	}

	// assert s3 objects should be deleted.
	for key, content := range S3Content {
		err := ensureS3Object(s3client, bucket, key, content)
		if err == nil {
			t.Errorf("File %v is not deleted in remote : %v\n", key, err)
		}
	}
}

// sync --delete s3://bucket/* s3://destbucket/
func TestSyncS3BucketToS3BucketWithDelete(t *testing.T) {
	t.Parallel()
	s3client, s5cmd, cleanup := setup(t)
	defer cleanup()

	bucket := s3BucketFromTestName(t)
	destbucket := "destbucket"
	createBucket(t, s3client, bucket)
	createBucket(t, s3client, destbucket)

	sourceS3Content := map[string]string{
		"readme.md":     "this is a readme file",
		"dir/main.py":   "this is a python file",
		"testfile1.txt": "this is a test file 1",
	}

	destS3Content := map[string]string{
		"main.md":       "this is a readme file",
		"dir/test.py":   "this is a python file",
		"testfile1.txt": "this is a test file 212321", // different size from source
		"testfile2.txt": "this is a test file 1",
	}

	for filename, content := range sourceS3Content {
		putFile(t, s3client, bucket, filename, content)
	}

	for filename, content := range destS3Content {
		putFile(t, s3client, destbucket, filename, content)
	}

	src := fmt.Sprintf("s3://%v/", bucket)
	dst := fmt.Sprintf("s3://%v/", destbucket)

	cmd := s5cmd("sync", "--delete", "--size-only", src+"*", dst)
	result := icmd.RunCmd(cmd)

	result.Assert(t, icmd.Success)

	assertLines(t, result.Stdout(), map[int]compareFunc{
		0: equals(`cp %vdir/main.py %vdir/main.py`, src, dst),
		1: equals(`cp %vreadme.md %vreadme.md`, src, dst),
		2: equals(`cp %vtestfile1.txt %vtestfile1.txt`, src, dst),
		3: equals(`rm %vdir/test.py`, dst),
		4: equals(`rm %vmain.md`, dst),
		5: equals(`rm %vtestfile2.txt`, dst),
	}, sortInput(true))

	expectedDestS3Content := map[string]string{
		"testfile1.txt": "this is a test file 1", // same as source bucket.
		"readme.md":     "this is a readme file",
		"dir/main.py":   "this is a python file",
	}

	nonExpectedDestS3Content := map[string]string{
		"dir/test.py":   "this is a python file",
		"main.md":       "this is a readme file",
		"testfile2.txt": "this is a test file 1",
	}

	// assert s3 objects in source.
	for key, content := range sourceS3Content {
		assert.Assert(t, ensureS3Object(s3client, bucket, key, content))
	}

	// assert s3 objects in destination. (should be)
	for key, content := range expectedDestS3Content {
		assert.Assert(t, ensureS3Object(s3client, destbucket, key, content))
	}

	// assert s3 objects should be deleted.
	for key, content := range nonExpectedDestS3Content {
		err := ensureS3Object(s3client, destbucket, key, content)
		if err == nil {
			t.Errorf("File %v is not deleted in remote : %v\n", key, err)
		}
	}
}

// sync s3://bucket/*.txt folder/
func TestSyncS3toLocalWithWildcard(t *testing.T) {
	t.Parallel()
	s3client, s5cmd, cleanup := setup(t)
	defer cleanup()

	bucket := s3BucketFromTestName(t)
	createBucket(t, s3client, bucket)

	// make local (destination) older.
	now := time.Now().UTC()
	timestamp := fs.WithTimestamps(
		now.Add(-time.Minute), // access time
		now.Add(-time.Minute), // mod time
	)

	// even though test.py exists in the source, since we used
	// wildcard operation '*.txt', test.py will not be in the source,
	// because all of the source files will be with extension '*.txt'.
	// therefore test.py will be deleted.
	folderLayout := []fs.PathOp{
		fs.WithFile("test.py", "this is a python file", timestamp),
		fs.WithFile("test.txt", "this is a test file", timestamp),
	}

	s3Content := map[string]string{
		"test.txt":          "this is a test file",
		"readme.md":         "this is a readve file",
		"main.py":           "py file",
		"subfolder/sub.txt": "yet another txt",
		"test.py":           "this is a python file",
	}

	for filename, content := range s3Content {
		putFile(t, s3client, bucket, filename, content)
	}

	workdir := fs.NewDir(t, "somedir", folderLayout...)
	defer workdir.Remove()

	src := fmt.Sprintf("s3://%v/", bucket)
	dst := fmt.Sprintf("%v/", workdir.Path())
	dst = filepath.ToSlash(dst)

	cmd := s5cmd("--log", "debug", "sync", "--delete", src+"*.txt", dst)
	result := icmd.RunCmd(cmd)

	result.Assert(t, icmd.Success)

	assertLines(t, result.Stdout(), map[int]compareFunc{
		0: equals(`cp %vsubfolder/sub.txt %vsubfolder/sub.txt`, src, dst),
		1: equals(`cp %vtest.txt %vtest.txt`, src, dst),
		2: equals(`rm %vtest.py`, dst),
	}, sortInput(true))

	expectedLayout := []fs.PathOp{
		fs.WithFile("test.txt", "this is a test file"),
		fs.WithDir("subfolder",
			fs.WithFile("sub.txt", "yet another txt"),
		),
	}

	expected := fs.Expected(t, expectedLayout...)
	assert.Assert(t, fs.Equal(workdir.Path(), expected))
}

// sync --delete s3://bucket/* .
func TestSyncS3BucketToLocalWithDeleteFlag(t *testing.T) {
	t.Parallel()
	s3client, s5cmd, cleanup := setup(t)
	defer cleanup()

	bucket := s3BucketFromTestName(t)
	createBucket(t, s3client, bucket)

	s3Content := map[string]string{
		"test.txt": "this is a test file",
	}

	for filename, content := range s3Content {
		putFile(t, s3client, bucket, filename, content)
	}

	workdir := fs.NewDir(t, "somedir")
	defer workdir.Remove()

	src := fmt.Sprintf("s3://%v/", bucket)
	dst := fmt.Sprintf("%v/", workdir.Path())
	dst = filepath.ToSlash(dst)

	cmd := s5cmd("--log", "debug", "sync", "--delete", src+"*", dst)
	result := icmd.RunCmd(cmd)

	result.Assert(t, icmd.Success)

	assertLines(t, result.Stdout(), map[int]compareFunc{
		0: equals(`cp %vtest.txt %vtest.txt`, src, dst),
	}, sortInput(true))

	expectedLayout := []fs.PathOp{
		fs.WithFile("test.txt", "this is a test file"),
	}

	expected := fs.Expected(t, expectedLayout...)
	assert.Assert(t, fs.Equal(workdir.Path(), expected))
}
