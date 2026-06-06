// Copyright (C) by Ubaldo Porcheddu <ubaldo@eja.it>

#include <jni.h>
#include <stdlib.h>
#include <string.h>
#include <unistd.h>
#include <fcntl.h>
#include <dirent.h>
#include <errno.h>
#include <android/log.h>

#define LOG_TAG "EJA_JNI"
#define LOGE(...) __android_log_print(ANDROID_LOG_ERROR, LOG_TAG, __VA_ARGS__)

#ifdef __cplusplus
extern "C" {
#endif

static int create_subprocess_internal(char const* cmd, char const* cwd, char* const argv[], char** envp, int* pProcessId) {
    int ptm = open("/dev/ptmx", O_RDWR | O_CLOEXEC);
    if (ptm < 0) {
        LOGE("FAILED open /dev/ptmx: %s", strerror(errno));
        return -1;
    }

    char* devname = ptsname(ptm);
    if (!devname || grantpt(ptm) || unlockpt(ptm)) {
        LOGE("FAILED pts setup: %s", strerror(errno));
        close(ptm);
        return -1;
    }

    pid_t pid = fork();
    if (pid < 0) {
        LOGE("FAILED fork: %s", strerror(errno));
        close(ptm);
        return -1;
    } else if (pid > 0) {
        *pProcessId = (int) pid;
        return ptm;
    } else {
        close(ptm);
        setsid();

        int pts = open(devname, O_RDWR);
        if (pts >= 0) {
            dup2(pts, 0);
            dup2(pts, 1);
            dup2(pts, 2);
            if (pts > 2) close(pts);
        } else {
            exit(1);
        }

        DIR* self_dir = opendir("/proc/self/fd");
        if (self_dir != NULL) {
            int self_dir_fd = dirfd(self_dir);
            struct dirent* entry;
            while ((entry = readdir(self_dir)) != NULL) {
                int fd = atoi(entry->d_name);
                if (fd > 2 && fd != self_dir_fd) close(fd);
            }
            closedir(self_dir);
        }

        if (envp) {
            clearenv();
            for (; *envp; ++envp) putenv(*envp);
        }

        if (cwd) chdir(cwd);

        execvp(cmd, argv);
        exit(1);
    }
}

JNIEXPORT jint JNICALL Java_it_eja_wikilite_Server_createSubprocess(
        JNIEnv* env, jclass clazz,
        jstring cmd, jstring cwd, jobjectArray args, jobjectArray envVars, jintArray processIdArray) {

    const char* cmd_utf8 = (*env)->GetStringUTFChars(env, cmd, NULL);
    const char* cwd_utf8 = cwd ? (*env)->GetStringUTFChars(env, cwd, NULL) : NULL;

    jsize args_size = args ? (*env)->GetArrayLength(env, args) : 0;
    char** argv = (char**) malloc((args_size + 2) * sizeof(char*));
    argv[0] = (char*) cmd_utf8;
    for (int i = 0; i < args_size; ++i) {
        jstring arg_obj = (jstring) (*env)->GetObjectArrayElement(env, args, i);
        argv[i + 1] = (char*) (*env)->GetStringUTFChars(env, arg_obj, NULL);
        (*env)->DeleteLocalRef(env, arg_obj);
    }
    argv[args_size + 1] = NULL;

    jsize env_size = envVars ? (*env)->GetArrayLength(env, envVars) : 0;
    char** envp = NULL;
    if (env_size > 0) {
        envp = (char**) malloc((env_size + 1) * sizeof(char*));
        for (int i = 0; i < env_size; ++i) {
            jstring env_obj = (jstring) (*env)->GetObjectArrayElement(env, envVars, i);
            envp[i] = (char*) (*env)->GetStringUTFChars(env, env_obj, NULL);
            (*env)->DeleteLocalRef(env, env_obj);
        }
        envp[env_size] = NULL;
    }

    int procId = 0;
    int ptm = create_subprocess_internal(cmd_utf8, cwd_utf8, argv, envp, &procId);

    for (int i = 0; i < args_size; ++i) {
        jstring arg_obj = (jstring) (*env)->GetObjectArrayElement(env, args, i);
        (*env)->ReleaseStringUTFChars(env, arg_obj, argv[i+1]);
        (*env)->DeleteLocalRef(env, arg_obj);
    }
    free(argv);

    if (envp) {
        for (int i = 0; i < env_size; ++i) {
             jstring env_obj = (jstring) (*env)->GetObjectArrayElement(env, envVars, i);
             (*env)->ReleaseStringUTFChars(env, env_obj, envp[i]);
             (*env)->DeleteLocalRef(env, env_obj);
        }
        free(envp);
    }

    if (cwd_utf8) (*env)->ReleaseStringUTFChars(env, cwd, cwd_utf8);
    (*env)->ReleaseStringUTFChars(env, cmd, cmd_utf8);

    if (ptm >= 0 && processIdArray != NULL) {
        (*env)->SetIntArrayRegion(env, processIdArray, 0, 1, &procId);
    }

    return ptm;
}

#ifdef __cplusplus
}
#endif
