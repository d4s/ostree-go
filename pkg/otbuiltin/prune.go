package otbuiltin

import (
       "unsafe"
       "time"
       "strings"
       "strconv"
       "bytes"
       "errors"


       glib "github.com/14rcole/ostree-go/pkg/glibobject"
)

// #cgo pkg-config: ostree-1
// #include <stdlib.h>
// #include <glib.h>
// #include <ostree.h>
// #include "builtin.go.h"
import "C"

var pruneOpts pruneOptions

type pruneOptions struct {
  NoPrune           bool      // Only display unreachable objects; don't delete
  RefsOnly          bool      // Only compute reachability via refs
  DeleteCommit      string    // Specify a commit to delete
  KeepYoungerThan   time.Time // All commits older than this date will be pruned
  Depth             int       // Only traverse depths (integer) parents for each commit (default: -1=infinite)
  StaticDeltasOnly  int       // Change the behavior of --keep-younger-than and --delete-commit to prune only the static delta files
}

func NewPruneOptions() pruneOptions {
  po := new(pruneOptions)
  po.Depth = -1
  return *po
}

func Prune(repoPath string, options pruneOptions) (string, error) {
  pruneOpts = options
  // attempt to open the repository
  repo, err := openRepo(repoPath)
  if err != nil {
    return "", err
  }

  var pruneFlags C.OstreeRepoPruneFlags
  var numObjectsTotal int
  var numObjectsPruned int
  var objSizeTotal  uint64
  var gerr = glib.NewGError()
  var cerr = (*C.GError)(gerr.Ptr())
  var cancellable *glib.GCancellable

  if !pruneOpts.NoPrune && !glib.GoBool(glib.GBoolean(C.ostree_repo_is_writable(repo.native(), &cerr))) {
    return "", glib.ConvertGError(glib.ToGError(unsafe.Pointer(cerr)))
  }

  cerr = nil
  if strings.Compare(pruneOpts.DeleteCommit, "") != 0 {
    if pruneOpts.NoPrune {
      return "", errors.New("Cannot specify both pruneOptions.DeleteCommit and pruneOptions.NoPrune")
    }

    if pruneOpts.StaticDeltasOnly  > 0 {
      if glib.GoBool(glib.GBoolean(C.ostree_repo_prune_static_deltas(repo.native(), C.CString(pruneOpts.DeleteCommit), (*C.GCancellable)(cancellable.Ptr()), &cerr))) {
        return "", glib.ConvertGError(glib.ToGError(unsafe.Pointer(cerr)))
      }
    } else if err = deleteCommit(repo, pruneOpts.DeleteCommit, cancellable); err != nil {
      return "", err
    }
  }

  if !pruneOpts.KeepYoungerThan.IsZero() {
    if pruneOpts.NoPrune {
      return "", errors.New("Cannot specify both pruneOptions.KeepYoungerThan and pruneOptions.NoPrune")
    }

    if err = pruneCommitsKeepYoungerThanDate(repo, pruneOpts.KeepYoungerThan, cancellable); err != nil {
      return "", err
    }
  }

  if pruneOpts.RefsOnly {
    pruneFlags |= C.OSTREE_REPO_PRUNE_FLAGS_REFS_ONLY
  }
  if pruneOpts.NoPrune {
    pruneFlags |= C.OSTREE_REPO_PRUNE_FLAGS_NO_PRUNE
  }

  formattedFreedSize := C.GoString((*C.char)(C.g_format_size_full((C.guint64)(objSizeTotal), 0)))

  var buffer bytes.Buffer

  buffer.WriteString("Total objects: ")
  buffer.WriteString(strconv.Itoa(numObjectsTotal))
  if numObjectsPruned == 0 {
    buffer.WriteString("\nNo unreachable objects")
  } else if pruneOpts.NoPrune {
    buffer.WriteString("\nWould delete: ")
    buffer.WriteString(strconv.Itoa(numObjectsPruned))
    buffer.WriteString(" objects, freeing ")
    buffer.WriteString(formattedFreedSize)
  } else {
    buffer.WriteString("\nDeleted ")
    buffer.WriteString(strconv.Itoa(numObjectsPruned))
    buffer.WriteString(" objects, ")
    buffer.WriteString(formattedFreedSize)
    buffer.WriteString(" freed")
  }

  return buffer.String(), nil
}

func deleteCommit(repo *Repo, commitToDelete string, cancellable *glib.GCancellable) error {
  var refs *glib.GHashTable
  var hashIter glib.GHashTableIter
  var hashkey, hashvalue C.gpointer
  var gerr = glib.NewGError()
  var cerr = (*C.GError)(gerr.Ptr())

  if glib.GoBool(glib.GBoolean(C.ostree_repo_list_refs(repo.native(), nil, (**C.GHashTable)(refs.Ptr()), (*C.GCancellable)(cancellable.Ptr()), &cerr))) {
    return glib.ConvertGError(glib.ToGError(unsafe.Pointer(cerr)))
  }

  C.g_hash_table_iter_init((*C.GHashTableIter)(hashIter.Ptr()), (*C.GHashTable)(refs.Ptr()))
  for C.g_hash_table_iter_next((*C.GHashTableIter)(hashIter.Ptr()), &hashkey, &hashvalue) != 0 {
    var ref string = C.GoString((*C.char)(hashkey))
    var commit string = C.GoString((*C.char)(hashvalue))
    if strings.Compare(commitToDelete, commit) == 0 {
      var buffer bytes.Buffer
      buffer.WriteString("Commit ")
      buffer.WriteString(commitToDelete)
      buffer.WriteString(" is referenced by ")
      buffer.WriteString(ref)
      return errors.New(buffer.String())
    }
  }

  if err := enableTombstoneCommits(repo); err != nil {
    return err
  }

  if !glib.GoBool(glib.GBoolean(C.ostree_repo_delete_object(repo.native(), C.OSTREE_OBJECT_TYPE_COMMIT, C.CString(commitToDelete), (*C.GCancellable)(cancellable.Ptr()), &cerr))) {
    return glib.ConvertGError(glib.ToGError(unsafe.Pointer(cerr)))
  }

  return nil
}

func pruneCommitsKeepYoungerThanDate(repo *Repo, date time.Time, cancellable *glib.GCancellable) error {
  var objects *glib.GHashTable
  var hashIter glib.GHashTableIter
  var key, value C.gpointer
  var gerr = glib.NewGError()
  var cerr = (*C.GError)(gerr.Ptr())

  if err := enableTombstoneCommits(repo); err != nil {
    return err
  }

  if !glib.GoBool(glib.GBoolean(C.ostree_repo_list_objects(repo.native(), C.OSTREE_REPO_LIST_OBJECTS_ALL, (**C.GHashTable)(objects.Ptr()), (*C.GCancellable)(cancellable.Ptr()), &cerr))) {
    return glib.ConvertGError(glib.ToGError(unsafe.Pointer(cerr)))
  }

  C.g_hash_table_iter_init((*C.GHashTableIter)(hashIter.Ptr()), (*C.GHashTable)(objects.Ptr()))
  for C.g_hash_table_iter_next((*C.GHashTableIter)(hashIter.Ptr()), &key, &value) != 0 {
    var serializedKey *glib.GVariant
    var checksum *C.char
    var objType C.OstreeObjectType
    var commitTimestamp uint64
    var commit *glib.GVariant = nil

    C.ostree_object_name_deserialize ((*C.GVariant)(serializedKey.Ptr()), &checksum, &objType)

    if objType != C.OSTREE_OBJECT_TYPE_COMMIT {
      continue
    }

    cerr = nil
    if !glib.GoBool(glib.GBoolean(C.ostree_repo_load_variant(repo.native(), C.OSTREE_OBJECT_TYPE_COMMIT, checksum, (**C.GVariant)(commit.Ptr()), &cerr))) {
      return glib.ConvertGError(glib.ToGError(unsafe.Pointer(cerr)))
    }

    commitTimestamp = (uint64)(C.ostree_commit_get_timestamp((*C.GVariant)(commit.Ptr())))
    if commitTimestamp < (uint64)(date.Unix()) {
      cerr = nil
      if pruneOpts.StaticDeltasOnly != 0 {
        if !glib.GoBool(glib.GBoolean(C.ostree_repo_prune_static_deltas(repo.native(), checksum, (*C.GCancellable)(cancellable.Ptr()), &cerr))) {
          return glib.ConvertGError(glib.ToGError(unsafe.Pointer(cerr)))
        }
      } else {
        if !glib.GoBool(glib.GBoolean(C.ostree_repo_delete_object(repo.native(), C.OSTREE_OBJECT_TYPE_COMMIT, checksum, (*C.GCancellable)(cancellable.Ptr()), &cerr))) {
          return glib.ConvertGError(glib.ToGError(unsafe.Pointer(cerr)))
        }
      }
    }
  }

  return nil
}
