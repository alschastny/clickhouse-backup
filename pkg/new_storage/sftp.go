package new_storage

import (
	"io"
	"io/ioutil"
	"path"
	"path/filepath"
	"syscall"
	"time"

	"github.com/AlexAkulov/clickhouse-backup/config"

	lib_sftp "github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

// Implement RemoteStorage
type SFTP struct {
	client   *lib_sftp.Client
	Config   *config.SFTPConfig
	dirCache map[string]struct{}
}

func (sftp *SFTP) Connect() error {
	f_sftp_key, err := ioutil.ReadFile(sftp.Config.Key)
	if err != nil {
		return err
	}
	sftp_key, err := ssh.ParsePrivateKey(f_sftp_key)
	if err != nil {
		return err
	}
	sftp_config := &ssh.ClientConfig{
		User: sftp.Config.Username,
		Auth: []ssh.AuthMethod{
			ssh.PublicKeys(sftp_key),
		},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
	}

	ssh_connection, _ := ssh.Dial("tcp", sftp.Config.Address+":22", sftp_config)
	// defer sftp_connection.Close()

	sftp_connection, err := lib_sftp.NewClient(ssh_connection)
	if err != nil {
		return err
	}
	// defer sftp_connection.Close()

	sftp.client = sftp_connection
	sftp.dirCache = map[string]struct{}{}
	return nil
}

func (sftp *SFTP) Kind() string {
	return "SFTP"
}

func (sftp *SFTP) StatFile(key string) (RemoteFile, error) {
	file_path := path.Join(sftp.Config.Path, key)

	stat, err := sftp.client.Stat(file_path)
	if err != nil {
		return nil, err
	}

	return &sftpFile{
		size:         stat.Size(),
		lastModified: stat.ModTime(),
		name:         stat.Name(),
	}, nil
}

func (sftp *SFTP) DeleteFile(key string) error {
	file_path := path.Join(sftp.Config.Path, key)

	file_stat, err := sftp.client.Stat(file_path)
	if err != nil {
		return err
	}
	if file_stat.IsDir() {
		return sftp.DeleteDirectory(file_path)
	} else {
		return sftp.client.Remove(file_path)
	}
}

func (sftp *SFTP) DeleteDirectory(dir_path string) error {
	defer sftp.client.RemoveDirectory(dir_path)

	files, err := sftp.client.ReadDir(dir_path)
	if err != nil {
		return err
	}
	for _, file := range files {
		file_path := path.Join(dir_path, file.Name())
		if file.IsDir() {
			sftp.DeleteDirectory(file_path)
		} else {
			defer sftp.client.Remove(file_path)
		}
	}

	return nil
}

func (sftp *SFTP) Walk(remote_path string, recursive bool, process func(RemoteFile) error) error {
	dir := path.Join(sftp.Config.Path, remote_path)

	if recursive {
		walker := sftp.client.Walk(dir)
		for walker.Step() {
			if err := walker.Err(); err != nil {
				return err
			}
			entry := walker.Stat()
			if entry == nil {
				continue
			}
			rel_name, _ := filepath.Rel(dir, walker.Path())
			err := process(&sftpFile{
				size:         entry.Size(),
				lastModified: entry.ModTime(),
				name:         rel_name,
			})
			if err != nil {
				return err
			}
		}
	} else {
		entries, err := sftp.client.ReadDir(dir)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			err := process(&sftpFile{
				size:         entry.Size(),
				lastModified: entry.ModTime(),
				name:         entry.Name(),
			})
			if err != nil {
				return err
			}
		}
	}
	return nil
}

func (sftp *SFTP) GetFileReader(key string) (io.ReadCloser, error) {
	file_path := path.Join(sftp.Config.Path, key)
	sftp.client.MkdirAll(path.Dir(file_path))
	return sftp.client.OpenFile(file_path, syscall.O_RDWR)
}

func (sftp *SFTP) PutFile(key string, local_file io.ReadCloser) error {
	file_path := path.Join(sftp.Config.Path, key)

	sftp.client.MkdirAll(path.Dir(file_path))

	remote_file, err := sftp.client.Create(file_path)
	if err != nil {
		return err
	}
	defer remote_file.Close()

	_, err = remote_file.ReadFrom(local_file)
	if err != nil {
		return err
	}

	return nil
}

// Implement RemoteFile
type sftpFile struct {
	size         int64
	lastModified time.Time
	name         string
}

func (file *sftpFile) Size() int64 {
	return file.size
}

func (file *sftpFile) LastModified() time.Time {
	return file.lastModified
}

func (file *sftpFile) Name() string {
	return file.name
}
