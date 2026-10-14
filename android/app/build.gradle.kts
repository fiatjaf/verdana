import org.gradle.kotlin.dsl.android

// release signing: a single password protects both the keystore and the key
val releaseStoreFile = System.getenv("ANDROID_KEYSTORE_PATH")
val releasePassword = System.getenv("ANDROID_KEYSTORE_PASSWORD")
val releaseKeyAlias = "verdana"
val releaseSigningConfigured = !releaseStoreFile.isNullOrBlank() && !releasePassword.isNullOrBlank()

if (gradle.startParameter.taskNames.any { it.contains("Release", ignoreCase = true) } && !releaseSigningConfigured) {
    throw GradleException("Release APK requires ANDROID_KEYSTORE_PATH and ANDROID_KEYSTORE_PASSWORD")
}

// version comes from the latest vMAJOR.MINOR.PATCH git tag: on the tagged commit itself
// versionName is "MAJOR.MINOR.PATCH", on later commits it is like "0.0.2-5-gabc1234"
val gitDescribe = runCatching {
    providers.exec {
        commandLine("git", "describe", "--tags", "--match", "v*")
        isIgnoreExitValue = true
    }.standardOutput.asText.get().trim()
}.getOrDefault("")
val tagVersion = Regex("""^v(\d+)\.(\d+)\.(\d+)""").find(gitDescribe)
val releaseVersionName = if (tagVersion != null) gitDescribe.removePrefix("v") else "0.0.0"
val releaseVersionCode = tagVersion?.destructured?.let { (major, minor, patch) ->
    require(minor.toInt() < 1000 && patch.toInt() < 1000) { "minor and patch must be < 1000" }
    major.toInt() * 1_000_000 + minor.toInt() * 1_000 + patch.toInt()
}?.coerceAtLeast(1) ?: 1

plugins {
    id("com.android.application")
    id("org.jetbrains.kotlin.android")
    id("org.jetbrains.kotlin.plugin.compose")
}

android {
    namespace = "com.verdana.app"
    compileSdk = 35

    defaultConfig {
        applicationId = "com.verdana.app"
        minSdk = 26
        targetSdk = 35
        this.versionCode = releaseVersionCode
        this.versionName = releaseVersionName
    }

    signingConfigs {
        create("release") {
            if (releaseSigningConfigured) {
                storeFile = file(releaseStoreFile!!)
                storePassword = releasePassword
                keyAlias = releaseKeyAlias
                keyPassword = releasePassword
            }
        }
    }

    buildTypes {
        release {
            isMinifyEnabled = false
            signingConfig = signingConfigs.getByName("release")
        }
    }
    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_11
        targetCompatibility = JavaVersion.VERSION_11
    }
    kotlinOptions {
        jvmTarget = "11"
    }
    buildFeatures {
        compose = true
    }
    packaging {
        jniLibs {
            useLegacyPackaging = true
        }
    }
}

dependencies {
    implementation(fileTree(mapOf("dir" to "libs", "include" to listOf("*.aar", "*.jar"))))

    val composeBom = platform("androidx.compose:compose-bom:2024.12.01")
    implementation(composeBom)
    implementation("androidx.compose.ui:ui")
    implementation("androidx.compose.ui:ui-tooling-preview")
    implementation("androidx.compose.material3:material3")
    implementation("androidx.activity:activity-compose:1.9.3")
    implementation("androidx.lifecycle:lifecycle-runtime-ktx:2.8.7")
    implementation("androidx.webkit:webkit:1.12.1")
    implementation("androidx.core:core-ktx:1.15.0")
    // pull-to-reload on napp windows
    implementation("androidx.swiperefreshlayout:swiperefreshlayout:1.1.0")
    // native "log in with google" (Credential Manager → ID token)
    implementation("androidx.credentials:credentials:1.3.0")
    implementation("androidx.credentials:credentials-play-services-auth:1.3.0")
    implementation("com.google.android.libraries.identity.googleid:googleid:1.1.1")
    // drag-and-drop reordering of the home tab's pinned napps
    implementation("sh.calvin.reorderable:reorderable:2.4.3")
    debugImplementation("androidx.compose.ui:ui-tooling")
}
