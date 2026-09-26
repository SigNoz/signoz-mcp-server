import os
import shutil
import subprocess
from dataclasses import dataclass, field
from pathlib import Path

import pytest

from fixtures.logger import setup_logger

logger = setup_logger(__name__)


@dataclass(frozen=True)
class Commander:
    executable: Path
    cwd: Path
    overrides: dict[str, str] = field(default_factory=dict)

    @classmethod
    def from_path(cls, value: str, cwd: Path, overrides: dict[str, str] | None = None) -> "Commander":
        executable = shutil.which(value)
        if executable is None:
            raise pytest.UsageError(f"required executable not found: {value}")
        return cls(executable=Path(executable), cwd=cwd, overrides=overrides or {})

    def run(self, *args: str, timeout: float | None = None) -> subprocess.CompletedProcess[str]:
        command = (str(self.executable), *args)
        logger.info("running: %s", " ".join(command))
        result = subprocess.run(
            command,
            check=False,
            cwd=self.cwd,
            env=os.environ | self.overrides,
            text=True,
            capture_output=True,
            timeout=timeout,
        )
        if result.returncode:
            raise RuntimeError(
                f"command failed ({result.returncode}): {' '.join(command)}\nstdout:\n{result.stdout}\nstderr:\n{result.stderr}"
            )
        return result


@pytest.fixture(scope="session")
def repository_dir() -> Path:
    return Path(__file__).resolve().parent.parent.parent
