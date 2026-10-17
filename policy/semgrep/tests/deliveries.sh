# shellcheck shell=bash
set -euo pipefail

# ok: nothing-installs-from-pypi-outside-take-delivery
# this used to be pipx install pre-commit, before the lock
# ruleid: nothing-installs-from-pypi-outside-take-delivery
python3 -m pip install --user "checkov==${CHECKOV_VERSION}"
# ruleid: nothing-installs-from-pypi-outside-take-delivery
uv tool install zizmor
# ok: nothing-installs-from-pypi-outside-take-delivery
echo "pip installed nothing"
# ok: nothing-installs-from-pypi-outside-take-delivery
scripts/take-delivery.sh checkov

# ruleid: the-installer-does-not-override-a-pinned-version
KUBECTL_VERSION="$(curl -fsSL https://dl.k8s.io/release/stable.txt)"
# ruleid: the-installer-does-not-override-a-pinned-version
TOFU_VERSION=$(cat .tofu-version)
# ok: the-installer-does-not-override-a-pinned-version
version="$(kubectl version --client)"
# ok: the-installer-does-not-override-a-pinned-version
echo "installing kubectl ${KUBECTL_VERSION}"
