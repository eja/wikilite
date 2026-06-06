// Copyright (C) by Ubaldo Porcheddu <ubaldo@eja.it>

package it.eja.wikilite

import android.annotation.SuppressLint
import android.content.Intent
import android.content.SharedPreferences
import android.os.Bundle
import android.util.Log
import android.view.View
import android.webkit.WebView
import android.webkit.WebViewClient
import android.widget.ProgressBar
import androidx.activity.OnBackPressedCallback
import androidx.appcompat.app.AppCompatActivity
import java.io.File

class MainActivity : AppCompatActivity() {

    private lateinit var webView: WebView
    private lateinit var progressBar: ProgressBar
    private lateinit var preferences: SharedPreferences
    private val DB_FILENAME = "wikilite.db"

    @SuppressLint("SetJavaScriptEnabled")
    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        setContentView(R.layout.activity_main)
        initializeAppLogic()
    }

    private fun initializeAppLogic() {
        preferences = getSharedPreferences("app_prefs", MODE_PRIVATE)

        val discoveredDb = discoverDatabase()

        if (discoveredDb != null) {
            preferences.edit().putString("db_path", discoveredDb.absolutePath).apply()
            initApp(discoveredDb.absolutePath)
        } else {
            val savedDbPath = preferences.getString("db_path", "")
            if (!savedDbPath.isNullOrEmpty() && File(savedDbPath).exists() && savedDbPath.endsWith(DB_FILENAME)) {
                initApp(savedDbPath)
            } else {
                startActivity(Intent(this, DatabaseDownloadActivity::class.java))
                finish()
            }
        }
    }

    private fun discoverDatabase(): File? {
        val externalDirs = getExternalFilesDirs(null)
        for (dir in externalDirs) {
            if (dir != null) {
                val dbFile = File(dir, DB_FILENAME)
                if (dbFile.exists()) {
                    return dbFile
                }
            }
        }

        val internalFile = File(filesDir, DB_FILENAME)
        if (internalFile.exists()) return internalFile

        return null
    }

    @SuppressLint("SetJavaScriptEnabled")
    private fun initApp(dbPath: String) {
        webView = findViewById(R.id.webView)
        progressBar = findViewById(R.id.progressBar)
        setupWebView()
        handleBackPress()

        try {
            Server.startBinaryServer(this, dbPath)

            pollServerAndLoadUrl()

        } catch (e: Exception) {
            Log.e("MainActivity", "Failed to setup and run wikilite", e)
            val errorMessage = "<html><body><h1>Error</h1><p>${e.message}</p></body></html>"
            webView.loadData(errorMessage, "text/html", "UTF-8")
        }
    }

    @SuppressLint("SetJavaScriptEnabled")
    private fun setupWebView() {
        webView.settings.javaScriptEnabled = true
        webView.settings.domStorageEnabled = true
        webView.settings.allowFileAccess = true
        webView.settings.allowContentAccess = true

        webView.webViewClient = object : WebViewClient() {
            override fun onPageStarted(view: WebView?, url: String?, favicon: android.graphics.Bitmap?) {
                super.onPageStarted(view, url, favicon)
                progressBar.visibility = View.VISIBLE
                webView.visibility = View.INVISIBLE
            }

            override fun onPageFinished(view: WebView, url: String) {
                super.onPageFinished(view, url)
                progressBar.visibility = View.GONE
                webView.visibility = View.VISIBLE
                Log.d("WebView", "Page finished: $url")
                Log.d("WebView", "Can go back: ${view.canGoBack()}")
            }
        }
    }

    private fun handleBackPress() {
        val callback = object : OnBackPressedCallback(true) {
            override fun handleOnBackPressed() {
                if (webView.canGoBack()) {
                    webView.goBack()
                } else {
                    isEnabled = false
                    onBackPressedDispatcher.onBackPressed()
                }
            }
        }
        onBackPressedDispatcher.addCallback(this, callback)
    }

    private fun pollServerAndLoadUrl(attempt: Int = 0) {
        if (attempt >= 15) { // Timeout after ~7.5 seconds (15 attempts * 500ms)
            runOnUiThread {
                webView.loadUrl("http://127.0.0.1:35248/")
            }
            return
        }

        Server.fetchStatus { isReady ->
            if (isReady) {
                runOnUiThread {
                    webView.loadUrl("http://127.0.0.1:35248/")
                }
            } else {
                webView.postDelayed({
                    pollServerAndLoadUrl(attempt + 1)
                }, 500)
            }
        }
    }

    override fun onDestroy() {
        super.onDestroy()
        Server.stopBinaryServer()
    }
}