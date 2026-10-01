package backend

import (
	"os"
	"path/filepath"
	"testing"
)

func storageTestInstance(artifact, instance string) *Instance {
	return &Instance{
		instance:        instance,
		storageInstance: instance,
		napp: Napp{
			ID:           "napplet~0123456789abcdef~storage-test",
			D:            "storage-test",
			Format:       FormatNapplet,
			Kind:         KindNapplet,
			ArtifactHash: artifact,
		},
	}
}

func TestNapStorageNamespaces(t *testing.T) {
	a := storageTestInstance("artifact-a", "instance-a")
	b := storageTestInstance("artifact-a", "instance-b")

	sharedA := napStoreID(&napCall{ci: a}, "shared")
	sharedB := napStoreID(&napCall{ci: b}, "")
	if sharedA != sharedB {
		t.Fatalf("same napplet version did not share storage: %q != %q", sharedA, sharedB)
	}
	if got := napStoreID(&napCall{ci: a}, "instance"); got == napStoreID(&napCall{ci: b}, "instance") {
		t.Fatalf("distinct instances shared storage namespace %q", got)
	}

	updated := storageTestInstance("artifact-b", "instance-a")
	if got := napStoreID(&napCall{ci: updated}, "shared"); got == sharedA {
		t.Fatalf("different artifact reused shared storage namespace %q", got)
	}

	reopened := storageTestInstance("artifact-a", "instance-a")
	if got, want := napStoreID(&napCall{ci: reopened}, "instance"), napStoreID(&napCall{ci: a}, "instance"); got != want {
		t.Fatalf("same logical instance changed namespace: %q != %q", got, want)
	}
	fresh := storageTestInstance("artifact-a", "instance-c")
	if got := napStoreID(&napCall{ci: fresh}, "instance"); got == napStoreID(&napCall{ci: a}, "instance") {
		t.Fatalf("fresh instance reused storage namespace %q", got)
	}
}

func TestNapStoragePersistenceFailureIsReturned(t *testing.T) {
	oldDataDir := dataDir
	dataDir = t.TempDir()
	t.Cleanup(func() {
		dataDir = oldDataDir
		storagesMu.Lock()
		storages = make(map[string]*nappStorage)
		storagesMu.Unlock()
	})

	blocked := filepath.Join(dataDir, "not-a-directory")
	if err := os.WriteFile(blocked, []byte("file"), 0600); err != nil {
		t.Fatal(err)
	}
	dataDir = blocked

	err := storageSetQuota("persistence-failure", "key", "value", nappletStorageQuota, errNappletQuota)
	if err == nil {
		t.Fatal("storage write succeeded despite unusable data directory")
	}
	if value, ok := storageGet("persistence-failure", "key"); ok {
		t.Fatalf("failed persistent write remained visible in memory: %q", value)
	}
}
