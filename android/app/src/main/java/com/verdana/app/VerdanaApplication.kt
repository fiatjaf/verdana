package com.verdana.app

import android.app.Application
import go.Seq

// The gomobile runtime wants a Context before anything else touches the
// bridge; pointing it at the application context once, here, is enough.
class VerdanaApplication : Application() {
    override fun onCreate() {
        super.onCreate()
        Seq.setContext(this)
    }
}
