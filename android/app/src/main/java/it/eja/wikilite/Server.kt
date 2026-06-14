// Copyright (C) by Ubaldo Porcheddu <ubaldo@eja.it>

package it.eja.wikilite

import android.content.Context
import android.net.Uri
import android.os.ParcelFileDescriptor
import android.util.Log
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import java.io.BufferedReader
import java.io.File
import java.io.FileInputStream
import java.io.InputStreamReader
import java.net.HttpURLConnection
import java.net.URL

object Server {

    @Volatile
    private var isStarted = false
    @Volatile
    private var hasFailed = false
    @Volatile
    private var currentPid = 0
    const val BASE_URL = "http://127.0.0.1:35248/"

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

    fun isStarted(): Boolean = isStarted
    fun hasFailed(): Boolean = hasFailed

    private fun getRawPathFromUri(uri: Uri): String? {
        if ("com.android.externalstorage.documents" == uri.authority) {
            try {
                val docId = android.provider.DocumentsContract.getDocumentId(uri) ?: return null
                val split = docId.split(":")
                if (split.size >= 2) {
                    val type = split[0]
                    val relativePath = split[1]
                    val baseDir = if ("primary".equals(type, ignoreCase = true)) {
                        android.os.Environment.getExternalStorageDirectory().absolutePath
                    } else {
                        "/storage/$type"
                    }
                    return File(baseDir, relativePath).absolutePath
                }
            } catch (e: Exception) {
                Log.e("Server", "Failed to parse document ID", e)
            }
        }
        return null
    }

    fun startBinaryServer(context: Context, dbPath: String) {
        if (isStarted) return
        isStarted = true
        hasFailed = false

        Thread {
            try {
                val libDir = context.applicationInfo.nativeLibraryDir
                val binPath = "$libDir/libwikilite.so"
                val appDir = context.filesDir.absolutePath
                if (!File(binPath).exists()) {
                    Log.e("Server", "Binary not found at $binPath")
                    hasFailed = true
                    return@Thread
                }

                val finalDbPath = if (dbPath.startsWith("content://")) {
                    val uri = Uri.parse(dbPath)
                    val rawPath = getRawPathFromUri(uri)
                    if (rawPath != null && File(rawPath).exists()) {
                        rawPath
                    } else {
                        dbPath
                    }
                } else {
                    dbPath
                }

                if (finalDbPath.startsWith("content://")) {
                    Log.e("Server", "Cannot run server with unresolved content:// URI")
                    hasFailed = true
                    return@Thread
                }

                Log.d("Server", "Starting server with DB path: $finalDbPath")

                val args = arrayOf(
                    "--db", finalDbPath,
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

                val fd = createSubprocess(binPath, appDir, args, env, pid)
                if (fd > 0) {
                    currentPid = pid[0]
                    val pfdSub = ParcelFileDescriptor.adoptFd(fd)
                    val input = FileInputStream(pfdSub.fileDescriptor)
                    val reader = BufferedReader(InputStreamReader(input))
                    try {
                        var line: String?
                        while (reader.readLine().also { line = it } != null) {
                            Log.d("wikilite", line ?: "")
                        }
                    } catch (e: java.io.IOException) {
                        Log.i("Server", "Process stream closed: ${e.message}")
                    }
                } else {
                    hasFailed = true
                }
            } catch (e: Exception) {
                Log.e("Server", "Exception occurred in binary server thread", e)
                hasFailed = true
            } finally {
                isStarted = false
                currentPid = 0
            }
        }.start()
    }

    fun restartBinaryServer(context: Context, dbPath: String) {
        stopBinaryServer()
        Thread.sleep(1000)
        startBinaryServer(context, dbPath)
    }

    suspend fun fetchStatus(): Boolean = withContext(Dispatchers.IO) {
        var conn: HttpURLConnection? = null
        try {
            val url = URL(BASE_URL)
            conn = url.openConnection() as HttpURLConnection
            conn.connectTimeout = 2000
            conn.readTimeout = 2000
            conn.requestMethod = "GET"
            conn.responseCode == 200
        } catch (e: Exception) {
            false
        } finally {
            conn?.disconnect()
        }
    }

    fun stopBinaryServer() {
        if (currentPid > 0) {
            android.os.Process.killProcess(currentPid)
            currentPid = 0
        }
        isStarted = false
        Log.d("Server", "Subprocess stopped.")
    }
}
