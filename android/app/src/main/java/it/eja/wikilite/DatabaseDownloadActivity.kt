// Copyright (C) by Ubaldo Porcheddu <ubaldo@eja.it>

package it.eja.wikilite

import android.app.Activity
import android.app.ProgressDialog
import android.content.Intent
import android.content.SharedPreferences
import android.net.Uri
import android.os.Build
import android.os.Bundle
import android.os.Environment
import android.provider.Settings
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
import java.net.HttpURLConnection
import java.net.URL
import java.util.zip.GZIPInputStream

class DatabaseDownloadActivity : AppCompatActivity() {

    private lateinit var recyclerView: androidx.recyclerview.widget.RecyclerView
    private lateinit var adapter: DatabaseFileAdapter
    private val databaseFiles = mutableListOf<String>()
    private lateinit var preferences: SharedPreferences
    private var progressDialog: ProgressDialog? = null

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
        showDbSelectionDialog()
    }

    override fun onDestroy() {
        super.onDestroy()
        activityScope.cancel()
        progressDialog?.dismiss()
    }

    override fun onCreateOptionsMenu(menu: Menu?): Boolean {
        menu?.add(0, 1, 0, "Select Existing DB")
        return true
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

    private fun handleDownloadRequest(filePath: String) {
        pendingFilePath = filePath
        val intent = Intent(Intent.ACTION_OPEN_DOCUMENT_TREE)
        selectFolderLauncher.launch(intent)
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
                        files.add(rfilename)
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
        val remoteFileName = currentFilePath.substringAfterLast('/')
        val localFileName = if (remoteFileName.endsWith(".gz")) remoteFileName.removeSuffix(".gz") else remoteFileName

        val pickedDir = DocumentFile.fromTreeUri(applicationContext, treeUri) ?: return@withContext null

        val finalFile = pickedDir.findFile(localFileName)
        finalFile?.delete()

        val createdFinalFile = pickedDir.createFile("application/octet-stream", localFileName) ?: return@withContext null
        val finalFileUri = createdFinalFile.uri

        var success = false
        var connection: HttpURLConnection? = null

        try {
            val url = URL("https://huggingface.co/datasets/eja/wikilite/resolve/main/$currentFilePath")
            connection = url.openConnection() as HttpURLConnection
            connection.connectTimeout = 15000
            connection.readTimeout = 15000
            connection.connect()

            if (connection.responseCode != HttpURLConnection.HTTP_OK) {
                return@withContext null
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

            val outputStream = contentResolver.openOutputStream(finalFileUri) ?: return@withContext null
            outputStream.use { fos ->
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

            success = true
            return@withContext finalFileUri

        } catch (e: Exception) {
            e.printStackTrace()
        } finally {
            connection?.disconnect()
            if (!success) {
                createdFinalFile.delete()
            }
        }

        return@withContext null
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