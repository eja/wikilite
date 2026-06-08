// Copyright (C) by Ubaldo Porcheddu <ubaldo@eja.it>

package it.eja.wikilite

import android.app.ActivityManager
import android.app.ProgressDialog
import android.content.Intent
import android.content.SharedPreferences
import android.os.Bundle
import android.os.Environment
import android.widget.Toast
import androidx.appcompat.app.AlertDialog
import androidx.appcompat.app.AppCompatActivity
import androidx.recyclerview.widget.LinearLayoutManager
import kotlinx.coroutines.*
import okhttp3.OkHttpClient
import okhttp3.Request
import org.json.JSONObject
import java.io.*
import java.net.HttpURLConnection
import java.net.URL
import java.util.zip.GZIPInputStream

class DatabaseDownloadActivity : AppCompatActivity() {

    private lateinit var recyclerView: androidx.recyclerview.widget.RecyclerView
    private lateinit var adapter: DatabaseFileAdapter
    private val databaseFiles = mutableListOf<String>()
    private lateinit var preferences: SharedPreferences
    private var progressDialog: ProgressDialog? = null
    private val DB_FILENAME = "wikilite.db"

    private val activityScope = CoroutineScope(Dispatchers.Main + SupervisorJob())
    private var lastProgressUpdateTime = 0L

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        setContentView(R.layout.activity_database_download)

        preferences = getSharedPreferences("app_prefs", MODE_PRIVATE)

        setupUI()
        loadDatabaseFiles()
    }

    override fun onDestroy() {
        super.onDestroy()
        activityScope.cancel()
        progressDialog?.dismiss()
    }

    private fun getRemovableStoragePath(): String? {
        val dirs = getExternalFilesDirs(null)
        for (dir in dirs) {
            if (dir != null && Environment.isExternalStorageRemovable(dir)) {
                return dir.absolutePath
            }
        }
        return null
    }

    private fun goToMainActivity() {
        startActivity(Intent(this, MainActivity::class.java))
        finish()
    }

    private fun setupUI() {
        recyclerView = findViewById(R.id.recyclerView)
        recyclerView.layoutManager = LinearLayoutManager(this)

        adapter = DatabaseFileAdapter(databaseFiles) { filePath ->
            handleDownloadRequest(filePath)
        }
        recyclerView.adapter = adapter
    }

    private fun handleDownloadRequest(filePath: String) {
        val sdCardPath = getRemovableStoragePath()
        val internalPath = filesDir.absolutePath

        if (sdCardPath != null) {
            AlertDialog.Builder(this)
                .setTitle("Select Storage")
                .setMessage("Where would you like to save the database?")
                .setPositiveButton("External") { _, _ ->
                    startDownload(filePath, sdCardPath)
                }
                .setNegativeButton("Internal") { _, _ ->
                    startDownload(filePath, internalPath)
                }
                .setNeutralButton("Cancel", null)
                .show()
        } else {
            startDownload(filePath, internalPath)
        }
    }

    private fun loadDatabaseFiles() {
        activityScope.launch {
            progressDialog = ProgressDialog(this@DatabaseDownloadActivity).apply {
                setMessage("Loading database files...")
                setCancelable(false)
                show()
            }

            val files = withContext(Dispatchers.IO) {
                loadFilesFromHuggingFace()
            }

            if (!isFinishing && !isDestroyed) {
                progressDialog?.dismiss()
                databaseFiles.clear()
                databaseFiles.addAll(files)
                adapter.notifyDataSetChanged()
            }
        }
    }

    private fun getAvailableMemoryGB(): Double {
        val activityManager = getSystemService(ACTIVITY_SERVICE) as ActivityManager
        val memoryInfo = ActivityManager.MemoryInfo()
        activityManager.getMemoryInfo(memoryInfo)
        return memoryInfo.availMem.toDouble() / (1024.0 * 1024.0 * 1024.0)
    }

    private fun loadFilesFromHuggingFace(): List<String> {
        val files = mutableListOf<String>()
        val availMemGB = getAvailableMemoryGB()
        val restrictToLexical = availMemGB <= 2.5

        if (restrictToLexical) {
            runOnUiThread {
                Toast.makeText(this@DatabaseDownloadActivity, "List restricted to lexical only because there is not enough free RAM.", Toast.LENGTH_LONG).show()
            }
        }

        try {
            val client = OkHttpClient()
            val request = Request.Builder()
                .url("https://huggingface.co/api/datasets/eja/wikilite")
                .build()

            val response = client.newCall(request).execute()
            val jsonResponse = response.body?.string()

            if (jsonResponse != null) {
                val jsonObject = JSONObject(jsonResponse)
                val siblings = jsonObject.getJSONArray("siblings")

                for (i in 0 until siblings.length()) {
                    val item = siblings.getJSONObject(i)
                    val rfilename = item.getString("rfilename")

                    if (rfilename.endsWith(".db.gz")) {
                        if (restrictToLexical) {
                            if (rfilename.startsWith("lexical")) {
                                files.add(rfilename)
                            }
                        } else {
                            files.add(rfilename)
                        }
                    }
                }
            }
        } catch (e: Exception) {
            e.printStackTrace()
        }
        return files
    }

    private fun startDownload(filePath: String, downloadPath: String) {
        activityScope.launch {
            showProgressPreparing()

            val success = downloadAndExtract(filePath, downloadPath)

            if (!isFinishing && !isDestroyed) {
                progressDialog?.dismiss()
                if (success) {
                    val finalDbPath = File(downloadPath, DB_FILENAME).absolutePath
                    preferences.edit().putString("db_path", finalDbPath).apply()
                    Toast.makeText(this@DatabaseDownloadActivity, "Download successful!", Toast.LENGTH_SHORT).show()
                    goToMainActivity()
                } else {
                    Toast.makeText(this@DatabaseDownloadActivity, "Download failed!", Toast.LENGTH_LONG).show()
                }
            }
        }
    }

    private fun showProgressPreparing() {
        progressDialog?.dismiss()
        progressDialog = ProgressDialog(this@DatabaseDownloadActivity).apply {
            setMessage("Preparing download...")
            setCancelable(false)
            setProgressStyle(ProgressDialog.STYLE_HORIZONTAL)
            isIndeterminate = false
            show()
        }
    }

    private suspend fun downloadAndExtract(currentFilePath: String, downloadPath: String): Boolean = withContext(Dispatchers.IO) {
        val tempFile = File(downloadPath, "$DB_FILENAME.tmp")
        val finalFile = File(downloadPath, DB_FILENAME)

        val dir = File(downloadPath)
        if (!dir.exists()) {
            dir.mkdirs()
        }

        if (tempFile.exists()) {
            tempFile.delete()
        }

        var success = false
        var connection: HttpURLConnection? = null

        try {
            val url = URL("https://huggingface.co/datasets/eja/wikilite/resolve/main/$currentFilePath")
            connection = url.openConnection() as HttpURLConnection
            connection.connectTimeout = 15000
            connection.readTimeout = 15000
            connection.connect()

            if (connection.responseCode != HttpURLConnection.HTTP_OK) {
                return@withContext false
            }

            val fileLength = connection.contentLength.toLong()
            val inputStream = connection.inputStream
            var totalExtractedBytes = 0L
            var bytesDownloaded = 0L

            val countingInputStream = object : FilterInputStream(inputStream) {
                override fun read(): Int {
                    val b = super.read()
                    if (b != -1) {
                        bytesDownloaded++
                        updateProgressOnMain(bytesDownloaded, fileLength, totalExtractedBytes)
                    }
                    return b
                }

                override fun read(b: ByteArray, off: Int, len: Int): Int {
                    val result = super.read(b, off, len)
                    if (result != -1) {
                        bytesDownloaded += result
                        updateProgressOnMain(bytesDownloaded, fileLength, totalExtractedBytes)
                    }
                    return result
                }
            }

            FileOutputStream(tempFile).use { fos ->
                GZIPInputStream(countingInputStream).use { gis ->
                    val buffer = ByteArray(65536)
                    var bytesRead: Int
                    while (gis.read(buffer).also { bytesRead = it } != -1) {
                        ensureActive()
                        fos.write(buffer, 0, bytesRead)
                        totalExtractedBytes += bytesRead
                    }
                }
            }

            if (tempFile.exists() && tempFile.length() > 0) {
                if (finalFile.exists()) {
                    finalFile.delete()
                }
                if (tempFile.renameTo(finalFile)) {
                    success = true
                }
            }

        } catch (e: Exception) {
            e.printStackTrace()
        } finally {
            connection?.disconnect()
            if (!success && tempFile.exists()) {
                tempFile.delete()
            }
        }

        return@withContext success
    }

    private fun updateProgressOnMain(downloaded: Long, totalDownload: Long, extracted: Long) {
        val currentTime = System.currentTimeMillis()
        if (currentTime - lastProgressUpdateTime < 300 && downloaded != totalDownload) {
            return
        }
        lastProgressUpdateTime = currentTime

        runOnUiThread {
            if (isFinishing || isDestroyed) return@runOnUiThread
            val extractedMB = extracted / (1024 * 1024)

            if (totalDownload > 0) {
                if (totalDownload > Int.MAX_VALUE) {
                    progressDialog?.max = (totalDownload / 1024).toInt()
                    progressDialog?.progress = (downloaded / 1024).toInt()
                } else {
                    progressDialog?.max = totalDownload.toInt()
                    progressDialog?.progress = downloaded.toInt()
                }
                progressDialog?.setMessage("Downloading...")
            } else {
                progressDialog?.setMessage("Extracted: ${extractedMB}MB")
            }
        }
    }
}