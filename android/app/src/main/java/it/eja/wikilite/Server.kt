// Copyright (C) by Ubaldo Porcheddu <ubaldo@eja.it>

package it.eja.wikilite

import android.content.Context
import android.os.ParcelFileDescriptor
import android.util.Log
import java.io.BufferedReader
import java.io.File
import java.io.FileInputStream
import java.io.InputStreamReader
import java.net.HttpURLConnection
import java.net.URL

object Server {

    private var isStarted = false
    private var currentPid = 0
    private const val BASE_URL = "http://127.0.0.1:35248/"

    init {
        System.loadLibrary("launcher")
    }

    @JvmStatic
    private external fun createSubprocess(
        cmd: String,
        cwd: String,
        args: Array<String>,
        envVars: Array<String>,
        processIdArray: IntArray
    ): Int

    fun startBinaryServer(context: Context, dbPath: String) {
        if (isStarted) return
        isStarted = true

        Thread {
            try {
                val libDir = context.applicationInfo.nativeLibraryDir
                val binPath = "$libDir/libwikilite.so"
                val appDir = context.filesDir.absolutePath
                if (!File(binPath).exists()) {
                    Log.e("Server", "Binary not found at $binPath")
                    return@Thread
                }

                val args = arrayOf(
                    "--db", dbPath,
                    "--web",
                    "--web-port", "35248",
                    "--web-host", "0.0.0.0"
                )

                val env = arrayOf(
                    "HOME=$appDir",
                    "TMPDIR=${context.cacheDir.absolutePath}",
                    "LD_LIBRARY_PATH=$libDir",
                    "PATH=$libDir"
                )
                val pid = IntArray(1)

                Log.d("Server", "Executing subprocess: $binPath with args ${args.joinToString(" ")}")
                val fd = createSubprocess(binPath, appDir, args, env, pid)
                if (fd > 0) {
                    currentPid = pid[0]
                    Log.d("Server", "Subprocess started successfully with PID: $currentPid")
                    val pfd = ParcelFileDescriptor.adoptFd(fd)
                    val input = FileInputStream(pfd.fileDescriptor)
                    val reader = BufferedReader(InputStreamReader(input))
                    var line: String?
                    while (reader.readLine().also { line = it } != null) {
                        Log.d("wikilite", line ?: "")
                    }
                } else {
                    Log.e("Server", "Failed to create subprocess. FD is negative: $fd")
                }
            } catch (e: Exception) {
                Log.e("Server", "Exception occurred in binary server thread", e)
            } finally {
                isStarted = false
                currentPid = 0
            }
        }.start()
    }

    fun restartBinaryServer(context: Context, dbPath: String) {
        if (currentPid > 0) {
            android.os.Process.killProcess(currentPid)
            currentPid = 0
            isStarted = false
        }
        Thread.sleep(200)
        startBinaryServer(context, dbPath)
    }

    fun fetchStatus(callback: (Boolean) -> Unit) {
        Thread {
            try {
                val url = URL(BASE_URL)
                val conn = url.openConnection() as HttpURLConnection
                conn.connectTimeout = 500
                conn.readTimeout = 500
                conn.requestMethod = "GET"

                if (conn.responseCode == 200) {
                    callback(true)
                    return@Thread
                }
            } catch (e: Exception) {
                Log.w("Server","Connection failed or timed out")
            }
            callback(false)
        }.start()
    }

    fun stopBinaryServer() {
        if (currentPid > 0) {
            android.os.Process.killProcess(currentPid)
            currentPid = 0
            isStarted = false
            Log.d("Server", "Subprocess stopped.")
        }
    }
}