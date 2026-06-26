// Copyright (C) by Ubaldo Porcheddu <ubaldo@eja.it>

package it.eja.wikilite

import android.app.Activity
import android.app.ProgressDialog
import android.content.Intent
import android.content.SharedPreferences
import android.content.pm.PackageManager
import android.net.Uri
import android.os.Bundle
import android.view.Menu
import android.view.MenuItem
import android.widget.Toast
import androidx.activity.result.contract.ActivityResultContracts
import androidx.appcompat.app.AlertDialog
import androidx.appcompat.app.AppCompatActivity
import androidx.documentfile.provider.DocumentFile
import androidx.recyclerview.widget.LinearLayoutManager
import kotlinx.coroutines.*
import okhttp3.OkHttpClient
import okhttp3.Request
import org.json.JSONObject
import java.io.*
import okio.Buffer
import okio.ForwardingSource
import okio.GzipSource
import okio.buffer
import okio.sink
import it.eja.wikilite.R

class DatabaseDownloadActivity : AppCompatActivity() {

    private lateinit var recyclerView: androidx.recyclerview.widget.RecyclerView
    private lateinit var adapter: DatabaseFileAdapter
    private val databaseFiles = mutableListOf<String>()
    private lateinit var preferences: SharedPreferences
    private var progressDialog: ProgressDialog? = null

    private val okHttpClient = OkHttpClient()
    private val activityScope = CoroutineScope(Dispatchers.Main + SupervisorJob())
    private var lastProgressUpdateTime = 0L
    private var pendingFilePath: String? = null

    private val selectFolderLauncher = registerForActivityResult(
        ActivityResultContracts.StartActivityForResult()
    ) { result ->
        if (result.resultCode == Activity.RESULT_OK) {
            val treeUri = result.data?.data
            if (treeUri != null) {
                contentResolver.takePersistableUriPermission(
                    treeUri,
                    Intent.FLAG_GRANT_READ_URI_PERMISSION or Intent.FLAG_GRANT_WRITE_URI_PERMISSION
                )
                pendingFilePath?.let { filePath ->
                    startDownload(filePath, treeUri)
                }
            }
        }
    }

    private val pickExistingDbLauncher = registerForActivityResult(
        ActivityResultContracts.StartActivityForResult()
    ) { result ->
        if (result.resultCode == Activity.RESULT_OK) {
            val uri = result.data?.data
            if (uri != null) {
                contentResolver.takePersistableUriPermission(
                    uri,
                    Intent.FLAG_GRANT_READ_URI_PERMISSION or Intent.FLAG_GRANT_WRITE_URI_PERMISSION
                )
                preferences.edit().putString("db_uri", uri.toString()).apply()
                Toast.makeText(this, "Database selected successfully!", Toast.LENGTH_SHORT).show()
                goToMainActivity()
            }
        } else {
            showDbSelectionDialog()
        }
    }

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        setContentView(R.layout.activity_database_download)

        preferences = getSharedPreferences("app_prefs", MODE_PRIVATE)

        setupUI()
        if (isManageExternalStorageDeclared()) {
            showDbSelectionDialog()
        } else {
            loadDatabaseFiles()
        }
    }

    override fun onDestroy() {
        super.onDestroy()
        activityScope.cancel()
        progressDialog?.dismiss()
    }

    override fun onCreateOptionsMenu(menu: Menu?): Boolean {
        if (isManageExternalStorageDeclared()) {
            menu?.add(0, 1, 0, "Select Existing DB")
            return true
        }
        return false
    }

    override fun onOptionsItemSelected(item: MenuItem): Boolean {
        if (item.itemId == 1) {
            val intent = Intent(Intent.ACTION_OPEN_DOCUMENT).apply {
                addCategory(Intent.CATEGORY_OPENABLE)
                type = "*/*"
            }
            pickExistingDbLauncher.launch(intent)
            return true
        }
        return super.onOptionsItemSelected(item)
    }

    private fun showDbSelectionDialog() {
        AlertDialog.Builder(this)
            .setTitle("Database Selection")
            .setMessage("Select a local database or download one online?")
            .setPositiveButton("Local") { _, _ ->
                val intent = Intent(Intent.ACTION_OPEN_DOCUMENT).apply {
                    addCategory(Intent.CATEGORY_OPENABLE)
                    type = "*/*"
                }
                pickExistingDbLauncher.launch(intent)
            }
            .setNegativeButton("Online") { _, _ ->
                loadDatabaseFiles()
            }
            .setCancelable(false)
            .show()
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

    private fun isManageExternalStorageDeclared(): Boolean {
        return try {
            @Suppress("DEPRECATION")
            val packageInfo = packageManager.getPackageInfo(packageName, PackageManager.GET_PERMISSIONS)
            packageInfo.requestedPermissions?.contains("android.permission.MANAGE_EXTERNAL_STORAGE") == true
        } catch (e: Exception) {
            false
        }
    }

    private fun handleDownloadRequest(filePath: String) {
        pendingFilePath = filePath
        if (isManageExternalStorageDeclared()) {
            val intent = Intent(Intent.ACTION_OPEN_DOCUMENT_TREE)
            selectFolderLauncher.launch(intent)
        } else {
            val defaultFolder = getExternalFilesDir(null) ?: filesDir
            startDownload(filePath, Uri.fromFile(defaultFolder))
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

    private fun loadFilesFromHuggingFace(): List<String> {
        val files = mutableListOf<String>()
        try {
            val request = Request.Builder()
                .url("https://huggingface.co/api/datasets/eja/wikilite")
                .build()

            okHttpClient.newCall(request).execute().use { response ->
                val jsonResponse = response.body?.string()
                if (jsonResponse != null) {
                    val jsonObject = JSONObject(jsonResponse)
                    val siblings = jsonObject.getJSONArray("siblings")

                    for (i in 0 until siblings.length()) {
                        val item = siblings.getJSONObject(i)
                        val rfilename = item.getString("rfilename")

                        if (rfilename.endsWith(".db.gz")) {
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

    private fun startDownload(filePath: String, treeUri: Uri) {
        activityScope.launch {
            showProgressPreparing()

            val finalFileUri = downloadAndExtract(filePath, treeUri)

            if (!isFinishing && !isDestroyed) {
                progressDialog?.dismiss()
                if (finalFileUri != null) {
                    preferences.edit().putString("db_uri", finalFileUri.toString()).apply()
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

    private suspend fun downloadAndExtract(currentFilePath: String, treeUri: Uri): Uri? = withContext(Dispatchers.IO) {
        if (!isManageExternalStorageDeclared()) {
            val defaultFolder = File(treeUri.path ?: "")
            val finalFile = File(defaultFolder, "wikilite.db")
            if (finalFile.exists()) {
                finalFile.delete()
            }
            finalFile.createNewFile()

            var success = false
            var response: okhttp3.Response? = null

            try {
                val url = "https://huggingface.co/datasets/eja/wikilite/resolve/main/$currentFilePath"
                val request = Request.Builder().url(url).build()
                response = okHttpClient.newCall(request).execute()

                if (!response.isSuccessful) {
                    return@withContext null
                }

                val body = response.body ?: return@withContext null
                val fileLength = body.contentLength()
                var totalExtractedBytes = 0L

                val progressSource = object : ForwardingSource(body.source()) {
                    var bytesDownloaded = 0L
                    override fun read(sink: Buffer, byteCount: Long): Long {
                        val bytesRead = super.read(sink, byteCount)
                        if (bytesRead != -1L) {
                            bytesDownloaded += bytesRead
                            updateProgressOnMain(bytesDownloaded, fileLength, totalExtractedBytes)
                        }
                        return bytesRead
                    }
                }

                val gzipSource = GzipSource(progressSource).buffer()
                val outputStream = FileOutputStream(finalFile)
                val sink = outputStream.sink().buffer()

                sink.use { bufferedSink ->
                    gzipSource.use { bufferedSource ->
                        val buffer = Buffer()
                        var read: Long
                        while (bufferedSource.read(buffer, 65536L).also { read = it } != -1L) {
                            ensureActive()
                            bufferedSink.write(buffer, read)
                            totalExtractedBytes += read
                        }
                    }
                }

                success = true
                return@withContext Uri.fromFile(finalFile)

            } catch (e: Exception) {
                e.printStackTrace()
            } finally {
                response?.close()
                if (!success) {
                    finalFile.delete()
                }
            }

            return@withContext null
        } else {
            val remoteFileName = currentFilePath.substringAfterLast('/')
            val localFileName = if (remoteFileName.endsWith(".gz")) remoteFileName.removeSuffix(".gz") else remoteFileName

            val pickedDir = DocumentFile.fromTreeUri(applicationContext, treeUri) ?: return@withContext null

            val finalFile = pickedDir.findFile(localFileName)
            finalFile?.delete()

            val createdFinalFile = pickedDir.createFile("application/octet-stream", localFileName) ?: return@withContext null
            val finalFileUri = createdFinalFile.uri

            var success = false
            var response: okhttp3.Response? = null

            try {
                val url = "https://huggingface.co/datasets/eja/wikilite/resolve/main/$currentFilePath"
                val request = Request.Builder().url(url).build()
                response = okHttpClient.newCall(request).execute()

                if (!response.isSuccessful) {
                    return@withContext null
                }

                val body = response.body ?: return@withContext null
                val fileLength = body.contentLength()
                var totalExtractedBytes = 0L

                val progressSource = object : ForwardingSource(body.source()) {
                    var bytesDownloaded = 0L
                    override fun read(sink: Buffer, byteCount: Long): Long {
                        val bytesRead = super.read(sink, byteCount)
                        if (bytesRead != -1L) {
                            bytesDownloaded += bytesRead
                            updateProgressOnMain(bytesDownloaded, fileLength, totalExtractedBytes)
                        }
                        return bytesRead
                    }
                }

                val gzipSource = GzipSource(progressSource).buffer()
                val outputStream = contentResolver.openOutputStream(finalFileUri) ?: return@withContext null
                val sink = outputStream.sink().buffer()

                sink.use { bufferedSink ->
                    gzipSource.use { bufferedSource ->
                        val buffer = Buffer()
                        var read: Long
                        while (bufferedSource.read(buffer, 65536L).also { read = it } != -1L) {
                            ensureActive()
                            bufferedSink.write(buffer, read)
                            totalExtractedBytes += read
                        }
                    }
                }

                success = true
                return@withContext finalFileUri

            } catch (e: Exception) {
                e.printStackTrace()
            } finally {
                response?.close()
                if (!success) {
                    createdFinalFile.delete()
                }
            }

            return@withContext null
        }
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
