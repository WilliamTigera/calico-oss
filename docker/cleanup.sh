#!/bin/bash
# Disable exit on non 0
set +e

# These packages are split up into chunks of dependent packages (more or less).
PACKAGES+=" python3-libs platform-python python3-libcomps platform-python-setuptools python3-rpm python3-dnf "
PACKAGES+=" python3-hawkey python3-libdnf python3-gpg "
PACKAGES+=" crypto-policies-scripts python3-setuptools-wheel python3-pip-wheel dnf nss-3.53.1-11.el8_2.i686 "
PACKAGES+=" nss-3.53.1-11.el8_2.x86_64 nss-sysinit yum libdnf"

# Remove systemd and dependencies.
PACKAGES+=" systemd systemd-udev systemd-pam dracut-squash dracut-network dracut dbus kexec-tools dhcp-client device-mapper"
PACKAGES+=" device-mapper-libs os-prober grub2-tools grub2-tools-minimal grubby libkcapi-hmaccalc libkcapi cryptsetup-libs"
PACKAGES+=" iputils trousers trousers-lib"

PACKAGES+=" json-c freetype fontconfig libpng kmod bind-export-libs rpm-build-libs kmod-libs openldap ima-evm-utils xz"
PACKAGES+=" libidn2 gnupg2 gnutls gnupg2 gpgme glib2 librepo elfutils-libs libsolv libmodulemd shadow-utils libsemanage"
PACKAGES+=" libsolv gettext-libs gettext libcroco cyrus-sasl-lib util-linux libpwquality pam libnsl2 libtirpc iproute"
PACKAGES+=" kbd"

PACKAGES+=" sqlite-libs-3.26.0-11.el8.i686 sqlite-libs-3.26.0-11.el8.x86_64 nss-softokn-3.53.1-11.el8_2.x86_64"
PACKAGES+=" nss-softokn-3.53.1-11.el8_2.i686 elfutils-default-yama-scope libyaml dbus-common dbus-tools dbus-libs dbus-daemon"
PACKAGES+=" tar libseccomp"

# Remove rpm and packages that rpm depends on.
PACKAGES+=" squashfs-tools libtasn1 lz4-libs bzip2-libs ca-certificates krb5-libs openssl-libs openssl openssl-pkcs11"
PACKAGES+=" libarchive libdb libdb-utils expat curl p11-kit-trust libcurl-minimal libzstd lua-libs elfutils-libelf"
PACKAGES+=" dbus-daemon libdb file-libs libdb-utils procps-ng libsmartcols libblkid libuuid libmount binutils readline"
PACKAGES+=" systemd-libs libcomps libmetalink file libfdisk gdbm gawk dhcp-libs openssl libdb libdb-utils expat curl"
PACKAGES+=" p11-kit p11-kit-trust libusbx rpm-libs rpm"

echo "$PACKAGES"

if ! PACKAGE_FILES=$(rpm -ql ${PACKAGES} | tr '\n' ' '); then
  echo "failed to list package files"
  exit 1
fi

if ! rpm -e ${PACKAGES}; then
  echo "failed to remove packages"
  exit 1
fi

# We don't care if rm fails, we're just making a best effort to remove any left over cruft from the erased packages.
rm -rf ${PACKAGE_FILES}
rm -rf /var/log/*

# Delete this script
rm "$0"

exit 0
