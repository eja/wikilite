plugins {
    id("com.android.application")
    id("kotlin-android")
}

android {
    namespace="it.eja.wikilite"
    compileSdk = 33

    defaultConfig {
        applicationId = "it.eja.wikilite"
        minSdk = 21
        targetSdk = 35
        versionCode = 2
        versionName = "1.1.18"
    }

    buildTypes {
        release {
            isMinifyEnabled = false
            proguardFiles(
                getDefaultProguardFile("proguard-android-optimize.txt"),
                "proguard-rules.pro"
            )
        }
    }

    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_1_8
        targetCompatibility = JavaVersion.VERSION_1_8
    }

    kotlinOptions {
        jvmTarget = "1.8"
    }

    externalNativeBuild {
        ndkBuild {
            path = file("src/main/java/com/termux/terminal/jni/Android.mk")
        }
    }

}

dependencies {
    implementation("androidx.appcompat:appcompat:1.6.1")
    implementation("com.google.android.material:material:1.9.0")
    implementation("androidx.constraintlayout:constraintlayout:2.1.4")
    implementation("androidx.preference:preference-ktx:1.2.1")
    implementation("com.squareup.okhttp3:okhttp:4.11.0")
}
