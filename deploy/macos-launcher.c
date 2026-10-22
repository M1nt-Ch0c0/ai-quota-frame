/* The native bundle executable gives launchd a stable responsible application
 * for the macOS Local Network prompt. Credentials remain in the private runtime
 * directory, outside the signed application bundle. */
#include <limits.h>
#include <pwd.h>
#include <stdio.h>
#include <stdlib.h>
#include <unistd.h>

int main(int argc, char **argv) {
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
    /* CLI commands use the same app identity, private environment and Bluetooth
     * permission as launchd. No credential is supplied in the arguments. */
    char **args = calloc((size_t)argc + 2, sizeof(*args));
    if (args == NULL) {
        return 1;
    }
    args[0] = "sh";
    args[1] = "./start.sh";
    for (int i = 1; i < argc; i++) {
        args[i + 1] = argv[i];
    }
    execv("/bin/sh", args);
    perror("PhotoPainter launcher");
    free(args);
    return 1;
}
