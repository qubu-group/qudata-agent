#include <stdio.h>
#include <string.h>
#include <strings.h>
#include <unistd.h>
#include <sys/wait.h>
#include <sys/stat.h>
#include <errno.h>
#include <fcntl.h>

static void secure_zero(void *ptr, size_t len) {
    volatile unsigned char *p = ptr;
    while (len--) *p++ = 0;
}

static int execute_command(char *const argv[]) {
    pid_t pid = fork();
    if (pid == -1) return -1;
    
    if (pid == 0) {
        execvp(argv[0], argv);
        _exit(127);
    }
    
    int status;
    if (waitpid(pid, &status, 0) == -1) return -1;
    return WIFEXITED(status) ? WEXITSTATUS(status) : -1;
}

static int execute_with_stdin(char *const argv[], const char *input, size_t len) {
    int pipefd[2];
    if (pipe(pipefd) == -1) return -1;
    
    pid_t pid = fork();
    if (pid == -1) {
        close(pipefd[0]);
        close(pipefd[1]);
        return -1;
    }
    
    if (pid == 0) {
        close(pipefd[1]);
        if (dup2(pipefd[0], STDIN_FILENO) == -1) _exit(127);
        close(pipefd[0]);
        
        int devnull = open("/dev/null", O_WRONLY);
        if (devnull != -1) {
            dup2(devnull, STDOUT_FILENO);
            dup2(devnull, STDERR_FILENO);
            close(devnull);
        }
        
        execvp(argv[0], argv);
        _exit(127);
    }
    
    close(pipefd[0]);
    ssize_t written = write(pipefd[1], input, len);
    close(pipefd[1]);
    
    if (written != (ssize_t)len) {
        waitpid(pid, NULL, 0);
        return -1;
    }
    
    int status;
    if (waitpid(pid, &status, 0) == -1) return -1;
    return WIFEXITED(status) ? WEXITSTATUS(status) : -1;
}

int luks_create_volume(const char *device_path, char *key, size_t key_len) {
    if (!device_path || !key || key_len == 0) {
        errno = EINVAL;
        return -1;
    }
    
    char *argv[] = {
        "cryptsetup", "luksFormat", "--type", "luks2",
        "--cipher", "aes-xts-plain64", "--key-size", "512",
        "--hash", "sha256", "--key-file", "-", "--batch-mode",
        (char *)device_path, NULL
    };
    
    int result = execute_with_stdin(argv, key, key_len);
    secure_zero(key, key_len);
    return result == 0 ? 0 : -1;
}

int luks_open_volume(const char *device_path, const char *mapper_name,
                     const char *mount_point, char *key, size_t key_len) {
    if (!device_path || !mapper_name || !mount_point || !key || key_len == 0) {
        errno = EINVAL;
        return -1;
    }
    
    char *open_argv[] = {
        "cryptsetup", "luksOpen", "--key-file", "-",
        (char *)device_path, (char *)mapper_name, NULL
    };
    
    int result = execute_with_stdin(open_argv, key, key_len);
    secure_zero(key, key_len);
    if (result != 0) return -1;
    
    char mapper_path[256];
    snprintf(mapper_path, sizeof(mapper_path), "/dev/mapper/%s", mapper_name);
    
    char *mkfs_argv[] = {"mkfs.ext4", "-F", "-q", mapper_path, NULL};
    execute_command(mkfs_argv);
    
    mkdir(mount_point, 0700);
    
    char *mount_argv[] = {"mount", "-t", "ext4", mapper_path, (char *)mount_point, NULL};
    result = execute_command(mount_argv);
    
    if (result != 0) {
        char *close_argv[] = {"cryptsetup", "luksClose", (char *)mapper_name, NULL};
        execute_command(close_argv);
        return -1;
    }
    
    return 0;
}

int luks_close_volume(const char *mount_point, const char *mapper_name) {
    if (!mount_point || !mapper_name) {
        errno = EINVAL;
        return -1;
    }
    
    char *umount_argv[] = {"umount", "-f", (char *)mount_point, NULL};
    execute_command(umount_argv);
    
    char *close_argv[] = {"cryptsetup", "luksClose", (char *)mapper_name, NULL};
    return execute_command(close_argv) == 0 ? 0 : -1;
}

int luks_is_open(const char *mapper_name) {
    if (!mapper_name) return 0;
    char *argv[] = {"cryptsetup", "status", (char *)mapper_name, NULL};
    return execute_command(argv) == 0 ? 1 : 0;
}
