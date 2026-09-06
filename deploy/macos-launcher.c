/* The native bundle executable gives launchd a stable responsible application
 * for the macOS Local Network prompt. Credentials remain in the private runtime
 * directory, outside the signed application bundle. */
#include <limits.h>
#include <pwd.h>
#include <stdio.h>
#include <unistd.h>

int main(void) {
    const struct passwd *user = getpwuid(getuid());
    char directory[PATH_MAX];
    if (user == NULL) {
        return 1;
    }
    int size = snprintf(directory, sizeof(directory),
                        "%s/Library/Application Support/ai-quota-frame",
                        user->pw_dir);
    if (size < 0 || (size_t)size >= sizeof(directory) || chdir(directory) != 0) {
        perror("PhotoPainter runtime directory");
        return 1;
    }
    execl("/bin/sh", "sh", "./start.sh", (char *)NULL);
    perror("PhotoPainter launcher");
    return 1;
}
