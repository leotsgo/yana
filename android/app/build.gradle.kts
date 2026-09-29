plugins {
    alias(libs.plugins.android.application)
    alias(libs.plugins.kotlin.compose)
    alias(libs.plugins.kotlin.serialization)
    alias(libs.plugins.ksp)
}

// The reader WebView's assets are a build artifact of the web toolchain
// (make android-reader, like the CRDT AAR): the page template, its
// script (the web's rich runtime bundled), and its stylesheet with the
// KaTeX fonts. Without them the reader cannot load; fail the build with
// the fix rather than shipping an app that renders nothing.
val readerAssetsDir = layout.projectDirectory.dir("src/main/assets/reader")
tasks.register("checkReaderAssets") {
    val dir = readerAssetsDir
    doLast {
        val missing = listOf("reader.html", "reader.js", "reader.css").filterNot { dir.file(it).asFile.exists() }
        if (missing.isNotEmpty()) {
            throw GradleException(
                "reader assets missing: ${missing.joinToString()} — run `make android-reader` at the repo root",
            )
        }
    }
}
tasks.named("preBuild") { dependsOn("checkReaderAssets") }

android {
    namespace = "com.collinpendleton.yana"
    compileSdk = 37

    defaultConfig {
        applicationId = "com.collinpendleton.yana"
        minSdk = 26
        targetSdk = 36
        versionCode = 1
        versionName = "0.1.0"
        testInstrumentationRunner = "androidx.test.runner.AndroidJUnitRunner"
    }

    // Native code is most of the app (the Go CRDT and render library,
    // bundled SQLite), so one APK per ABI instead of a universal APK
    // carrying every copy. The ABIs match make android-crdt.
    splits {
        abi {
            isEnable = true
            reset()
            include("arm64-v8a", "armeabi-v7a", "x86_64")
            isUniversalApk = false
        }
    }

    // Debug builds sign with this checked-in key (the standard debug
    // alias and passwords, nothing secret) instead of each machine's own
    // ~/.android/debug.keystore, so an APK from CI or any laptop installs
    // over one from another. A CI runner's key was new on every run.
    signingConfigs {
        getByName("debug") {
            storeFile = file("debug.keystore")
            storePassword = "android"
            keyAlias = "androiddebugkey"
            keyPassword = "android"
        }
    }

    buildTypes {
        debug {
            signingConfig = signingConfigs.getByName("debug")
            applicationIdSuffix = ".debug"
            versionNameSuffix = "-debug"
        }
        release {
            isMinifyEnabled = true
            isShrinkResources = true
            proguardFiles(getDefaultProguardFile("proguard-android-optimize.txt"), "proguard-rules.pro")
        }
    }

    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_17
        targetCompatibility = JavaVersion.VERSION_17
    }

    buildFeatures {
        compose = true
        buildConfig = true
    }

    lint {
        warningsAsErrors = false
        abortOnError = true
        // New library versions land through their own PRs, not as a lint
        // failure on unrelated work.
        disable += setOf("GradleDependency", "NewerVersionAvailable", "AndroidGradlePluginVersion")
        // targetSdk moves when the app has been run against the new release's behaviour changes.
        disable += "OldTargetApi"
    }

    testOptions {
        unitTests.isReturnDefaultValues = true
    }
}

dependencies {
    implementation(project(":crdt"))

    implementation(libs.androidx.core)
    implementation(libs.androidx.activity.compose)
    implementation(libs.androidx.lifecycle.runtime.compose)
    implementation(libs.androidx.lifecycle.process)
    implementation(libs.androidx.lifecycle.viewmodel.compose)
    implementation(libs.androidx.navigation.compose)
    implementation(libs.androidx.security.crypto)
    implementation(libs.androidx.webkit)
    // The home-screen widget (Phase 12f).
    implementation(libs.glance.appwidget)

    implementation(platform(libs.compose.bom))
    implementation(libs.compose.ui)
    implementation(libs.compose.ui.tooling.preview)
    implementation(libs.compose.material3)
    implementation(libs.compose.window.size)
    implementation(libs.compose.material.icons)
    debugImplementation(libs.compose.ui.tooling)

    implementation(libs.retrofit)
    implementation(libs.retrofit.kotlinx.serialization)
    implementation(libs.okhttp)
    implementation(libs.kotlinx.serialization.json)
    implementation(libs.kotlinx.coroutines.android)

    implementation(libs.room.runtime)
    implementation(libs.room.ktx)
    ksp(libs.room.compiler)
    implementation(libs.sqlite.bundled)
    implementation(libs.work.runtime.ktx)

    testImplementation(libs.junit)
    testImplementation(libs.okhttp.mockwebserver)
    testImplementation(libs.kotlinx.coroutines.test)

    androidTestImplementation(libs.androidx.test.core)
    androidTestImplementation(libs.androidx.test.ext.junit)
    androidTestImplementation(libs.androidx.test.runner)
    androidTestImplementation(libs.espresso.core)
    androidTestImplementation(platform(libs.compose.bom))
    androidTestImplementation(libs.compose.ui.test.junit4)
    androidTestImplementation(libs.okhttp.mockwebserver)
    androidTestImplementation(libs.kotlinx.coroutines.test)
}
