from __future__ import annotations

import os
from pathlib import Path
import stat


SENSITIVE_NAMES = frozenset(
    {
        ".netrc",
        ".npmrc",
        ".pypirc",
        "credentials",
        "credentials.json",
        "id_dsa",
        "id_ecdsa",
        "id_ed25519",
        "id_rsa",
        "known_hosts",
    }
)
SENSITIVE_DIRECTORIES = frozenset({".aws", ".git", ".gnupg", ".ssh"})
SENSITIVE_SUFFIXES = frozenset({".key", ".p12", ".pem", ".pfx"})


class PathPolicyError(ValueError):
    def __init__(self, code: str, message: str) -> None:
        super().__init__(message)
        self.code = code


class WorkspacePathGuard:
    def __init__(self, workspace_root: str, allowed_roots: tuple[str, ...]) -> None:
        if not allowed_roots:
            raise PathPolicyError("workspace_roots_required", "At least one workspace root is required")
        self.root = self._resolve_root(workspace_root)
        canonical_allowed = tuple(self._resolve_root(root) for root in allowed_roots)
        if self.root not in canonical_allowed:
            raise PathPolicyError("workspace_root_not_allowed", "Workspace root is not allowed")

    def resolve(
        self,
        raw_path: str,
        *,
        expected: str = "any",
        allow_missing: bool = False,
    ) -> Path:
        if not isinstance(raw_path, str) or not raw_path:
            raise PathPolicyError("invalid_path", "Path must be a non-empty string")
        if "\x00" in raw_path:
            raise PathPolicyError("invalid_path", "Path contains a NUL byte")
        if ".." in Path(raw_path).parts:
            raise PathPolicyError("path_traversal", "Parent path segments are not allowed")

        candidate = Path(raw_path).expanduser()
        if not candidate.is_absolute():
            candidate = self.root / candidate
        self._reject_symlink_components(candidate)
        try:
            resolved = candidate.resolve(strict=not allow_missing)
        except FileNotFoundError as exc:
            raise PathPolicyError("path_not_found", "Path does not exist") from exc
        self._require_within_root(resolved)
        self._reject_sensitive(resolved)

        if allow_missing and not resolved.exists():
            return resolved
        try:
            mode = resolved.lstat().st_mode
        except FileNotFoundError as exc:
            raise PathPolicyError("path_not_found", "Path does not exist") from exc
        if stat.S_ISLNK(mode):
            raise PathPolicyError("symlink_not_allowed", "Symbolic links are not allowed")
        if expected == "file" and not stat.S_ISREG(mode):
            raise PathPolicyError("not_regular_file", "Path is not a regular file")
        if expected == "directory" and not stat.S_ISDIR(mode):
            raise PathPolicyError("not_directory", "Path is not a directory")
        if expected == "any" and not (stat.S_ISREG(mode) or stat.S_ISDIR(mode)):
            raise PathPolicyError("special_file_not_allowed", "Special files are not allowed")
        return resolved

    def is_safe_entry(self, path: Path) -> bool:
        try:
            self._reject_symlink_components(path)
            resolved = path.resolve(strict=True)
            self._require_within_root(resolved)
            self._reject_sensitive(resolved)
            mode = resolved.lstat().st_mode
            return stat.S_ISREG(mode) or stat.S_ISDIR(mode)
        except (OSError, PathPolicyError):
            return False

    def relative(self, path: Path) -> str:
        return path.relative_to(self.root).as_posix() or "."

    def open_regular_file(self, path: Path) -> int:
        flags = os.O_RDONLY
        if hasattr(os, "O_NOFOLLOW"):
            flags |= os.O_NOFOLLOW
        try:
            descriptor = os.open(path, flags)
        except OSError as exc:
            raise PathPolicyError("file_open_failed", "File could not be opened safely") from exc
        mode = os.fstat(descriptor).st_mode
        if not stat.S_ISREG(mode):
            os.close(descriptor)
            raise PathPolicyError("not_regular_file", "Path is not a regular file")
        return descriptor

    @staticmethod
    def _resolve_root(raw_root: str) -> Path:
        try:
            root = Path(raw_root).expanduser().resolve(strict=True)
        except (OSError, RuntimeError) as exc:
            raise PathPolicyError("workspace_root_invalid", "Workspace root is invalid") from exc
        if not root.is_dir():
            raise PathPolicyError("workspace_root_invalid", "Workspace root must be a directory")
        return root

    def _require_within_root(self, path: Path) -> None:
        try:
            path.relative_to(self.root)
        except ValueError as exc:
            raise PathPolicyError("path_outside_workspace", "Path is outside the workspace") from exc

    def _reject_symlink_components(self, path: Path) -> None:
        absolute = path if path.is_absolute() else self.root / path
        current = Path(absolute.anchor)
        for part in absolute.parts[1:]:
            current /= part
            if not current.exists() and not current.is_symlink():
                continue
            try:
                if stat.S_ISLNK(current.lstat().st_mode):
                    raise PathPolicyError("symlink_not_allowed", "Symbolic links are not allowed")
            except FileNotFoundError:
                continue

    def _reject_sensitive(self, path: Path) -> None:
        relative_parts = tuple(part.lower() for part in path.relative_to(self.root).parts)
        for part in relative_parts:
            if part == ".env" or part.startswith(".env."):
                raise PathPolicyError("sensitive_path", "Sensitive paths cannot be read")
            if part in SENSITIVE_NAMES or part in SENSITIVE_DIRECTORIES:
                raise PathPolicyError("sensitive_path", "Sensitive paths cannot be read")
            if Path(part).suffix in SENSITIVE_SUFFIXES:
                raise PathPolicyError("sensitive_path", "Sensitive paths cannot be read")
        if len(relative_parts) >= 2 and relative_parts[-2:] == (".git", "config"):
            raise PathPolicyError("sensitive_path", "Git configuration cannot be read")
