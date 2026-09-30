package com.verdana.app

import android.app.Application
import go.Seq

// The gomobile runtime wants a Context before anything else touches the
// bridge; pointing it at the application context once, here, is enough.
//
// The backend starts here too, not in an activity: a napp window is its own
// activity, so which one the process happens to start from must not decide
// whether the backend is up. VerdanaHost holds it for the whole process.
class VerdanaApplication : Application() {
    override fun onCreate() {
        super.onCreate()
        Seq.setContext(this)
        VerdanaHost.start(this)
    }
}
