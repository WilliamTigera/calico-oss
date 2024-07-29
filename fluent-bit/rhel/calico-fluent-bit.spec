Name: calico-fluent-bit
Version: 3.1.4
Release: 1%{?dist}
Summary: Fluent Bit is a super fast, lightweight, and highly scalable logging and metrics processor and forwarder.
License: Apache-2.0
URL: https://github.com/fluent/fluent-bit
Source0: https://github.com/fluent/fluent-bit/archive/refs/tags/v%{version}.tar.gz

Patch0: 0001-Rename-Fluent-Bit-package-name-to-Calico-Fluent-Bit.patch
Patch1: 0002-Move-config-files-to-etc-calico-calico-fluent-bit.patch
Patch2: 0003-Use-Calico-specific-binary-name-and-config-for-syste.patch

BuildRequires: bison
BuildRequires: cmake
BuildRequires: flex
BuildRequires: gcc-c++
BuildRequires: libyaml-devel
BuildRequires: make
BuildRequires: openssl-devel
BuildRequires: pkgconfig
BuildRequires: zlib-devel

%description

Fluent Bit is a fast Log Processor and Forwarder for Linux, Embedded Linux, MacOS and BSD
family operating systems. It's part of the Fluentd Ecosystem and a CNCF sub-project.

%prep
%autosetup -p1

%build
%cmake \
    -DCMAKE_BUILD_TYPE=RelWithDebInfo \
    -DCMAKE_INSTALL_PREFIX=/usr \
    -DCMAKE_INSTALL_SYSCONFDIR=/etc \
    -DFLB_DEBUG=Off \
    -DFLB_EXAMPLES=Off \
    -DFLB_IN_TAIL=On \
    -DFLB_LUAJIT=Off \
    -DFLB_MINIMAL=On \
    -DFLB_RELEASE=On \
    -DFLB_SHARED_LIB=Off \
    -DFLB_SQLDB=Off \
    -DFLB_TESTS_INTERNAL=Off \
    -DFLB_TESTS_RUNTIME=Off \
    -DFLB_TLS=On \
    -DFLB_WASM=Off

%cmake_build

%install
%cmake_install
rm -rvf %{buildroot}%{_includedir}

%check
%ctest

%preun
%systemd_preun %{name}.service

%postun
%systemd_postun_with_restart %{name}.service

%files
%dir %{_sysconfdir}/calico/%{name}
%config(noreplace) %{_sysconfdir}/calico/%{name}/*.conf
%{_bindir}/%{name}
%{_unitdir}/%{name}.service

%changelog
* Sat Jul 27 2024 Jiawei Huang <jiawei@tigera.io> - 3.1.4-1
- Initial Calico Fluent Bit package
