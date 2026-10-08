package com.verdana.app

import android.app.Activity
import android.util.Log
import androidx.credentials.CredentialManager
import androidx.credentials.CustomCredential
import androidx.credentials.GetCredentialRequest
import androidx.credentials.exceptions.GetCredentialCancellationException
import androidx.credentials.exceptions.GetCredentialException
import com.google.android.libraries.identity.googleid.GetSignInWithGoogleOption
import com.google.android.libraries.identity.googleid.GoogleIdTokenCredential
import mobile.Mobile

// "Log in with Google" on Android: Credential Manager shows the system's
// Google account sheet and hands back an ID token, which the backend trades
// at Pomegranate central (/login/google/android) for central's own token and
// takes from there to a bunker, like the desktop's browser flow does.
object GoogleLogin {
    // The OAuth *web* client id Pomegranate central runs with (its
    // GoogleClientID): the ID token must be issued for it, central rejects
    // any other audience. The Android OAuth client (package name + signing
    // SHA-1) only has to exist in the same Google Cloud project.
    private const val SERVER_CLIENT_ID = "300561989816-7nv10jo4vdn0d6p9knf12g7rq4fcusnc.apps.googleusercontent.com"

    suspend fun login(activity: Activity) {
        val request = GetCredentialRequest.Builder()
            .addCredentialOption(GetSignInWithGoogleOption.Builder(SERVER_CLIENT_ID).build())
            .build()

        val credential = try {
            CredentialManager.create(activity).getCredential(activity, request).credential
        } catch (e: GetCredentialCancellationException) {
            return
        } catch (e: GetCredentialException) {
            Log.w("verdana", "google login failed", e)
            Mobile.loginFailed("Google login failed: ${e.message ?: e.type}")
            return
        }

        if (credential !is CustomCredential ||
            credential.type != GoogleIdTokenCredential.TYPE_GOOGLE_ID_TOKEN_CREDENTIAL
        ) {
            Mobile.loginFailed("Google gave back an unexpected credential.")
            return
        }
        Mobile.loginWithGoogle(GoogleIdTokenCredential.createFrom(credential.data).idToken)
    }
}
