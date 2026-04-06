plugins {
    id("com.android.application")
    id("org.jetbrains.kotlin.android")
    id("com.google.protobuf")
}

import org.jetbrains.kotlin.gradle.dsl.JvmTarget

val hasGoogleServicesJson = file("google-services.json").exists()

if (hasGoogleServicesJson) {
    apply(plugin = "com.google.gms.google-services")
}

android {
    namespace = "top.miceworld.pocketbridge"
    compileSdk = 35

    defaultConfig {
        applicationId = "top.miceworld.pocketbridge"
        minSdk = 29
        targetSdk = 35
        versionCode = 1
        versionName = "0.1.0"
    }

    buildTypes {
        release {
            isMinifyEnabled = false
            signingConfig = signingConfigs.getByName("debug")
        }
    }

    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_17
        targetCompatibility = JavaVersion.VERSION_17
    }

    buildFeatures {
        viewBinding = true
    }
}

kotlin {
    compilerOptions {
        jvmTarget.set(JvmTarget.JVM_17)
    }
}

dependencies {
    implementation(platform("com.google.firebase:firebase-bom:34.4.0"))
    implementation("androidx.core:core-ktx:1.15.0")
    implementation("androidx.appcompat:appcompat:1.7.0")
    implementation("androidx.activity:activity-ktx:1.10.1")
    implementation("androidx.lifecycle:lifecycle-runtime-ktx:2.8.7")
    implementation("com.google.android.material:material:1.12.0")
    implementation("com.google.firebase:firebase-messaging")
    implementation("com.squareup.okhttp3:okhttp:4.12.0")
    implementation("com.google.protobuf:protobuf-javalite:4.30.2")
    implementation("org.bouncycastle:bcprov-jdk18on:1.80")
    implementation("org.jetbrains.kotlinx:kotlinx-coroutines-android:1.9.0")
}

protobuf {
    protoc {
        artifact = "com.google.protobuf:protoc:4.30.2"
    }
    generateProtoTasks {
        all().forEach { task ->
            task.builtins {
                maybeCreate("java").apply {
                    option("lite")
                }
            }
        }
    }
}
