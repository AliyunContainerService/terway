//go:build privileged

package utils

import (
	"context"
	"net"
	"runtime"
	"testing"

	"github.com/containernetworking/plugins/pkg/ns"
	"github.com/containernetworking/plugins/pkg/testutils"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/vishvananda/netlink"

	"github.com/AliyunContainerService/terway/pkg/tc"
	terwayTypes "github.com/AliyunContainerService/terway/types"
)

func TestCNI(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Utils Suite")
}

var _ = Describe("Utils", func() {
	Describe("EnsureVlanUntagger", func() {
		It("should ensure vlan untagger successfully", func() {
			runtime.LockOSThread()
			defer runtime.UnlockOSThread()

			var err error
			hostNS, err := testutils.NewNS()
			Expect(err).NotTo(HaveOccurred())

			containerNS, err := testutils.NewNS()
			Expect(err).NotTo(HaveOccurred())

			err = hostNS.Set()
			Expect(err).NotTo(HaveOccurred())

			defer func() {
				err := containerNS.Close()
				Expect(err).NotTo(HaveOccurred())

				err = testutils.UnmountNS(containerNS)
				Expect(err).NotTo(HaveOccurred())

				err = hostNS.Close()
				Expect(err).NotTo(HaveOccurred())

				err = testutils.UnmountNS(hostNS)
				Expect(err).NotTo(HaveOccurred())
			}()

			err = netlink.LinkAdd(&netlink.Dummy{
				LinkAttrs: netlink.LinkAttrs{Name: "eni"},
			})
			Expect(err).NotTo(HaveOccurred())

			eni, err := netlink.LinkByName("eni")
			Expect(err).NotTo(HaveOccurred())

			err = EnsureVlanUntagger(context.Background(), eni)
			Expect(err).NotTo(HaveOccurred())
		})
	})

	Describe("Link operations", func() {
		var hostNS ns.NetNS
		const nicName = "test-nic"

		BeforeEach(func() {
			var err error
			hostNS, err = testutils.NewNS()
			Expect(err).NotTo(HaveOccurred())

			err = hostNS.Do(func(netNS ns.NetNS) error {
				defer GinkgoRecover()
				return netlink.LinkAdd(&netlink.Dummy{
					LinkAttrs: netlink.LinkAttrs{Name: nicName},
				})
			})
			Expect(err).NotTo(HaveOccurred())
		})

		AfterEach(func() {
			_ = hostNS.Do(func(netNS ns.NetNS) error {
				defer GinkgoRecover()
				link, err := netlink.LinkByName(nicName)
				if err == nil {
					err = netlink.LinkDel(link)
					Expect(err).NotTo(HaveOccurred())
				}
				return nil
			})
			Expect(hostNS.Close()).To(Succeed())
			Expect(testutils.UnmountNS(hostNS)).To(Succeed())
		})

		Describe("EnsureLinkUp", func() {
			It("should set link up when it is down", func() {
				var err error
				var changed bool

				err = hostNS.Do(func(netNS ns.NetNS) error {
					defer GinkgoRecover()

					link, err := netlink.LinkByName(nicName)
					Expect(err).NotTo(HaveOccurred())

					// Set link down first
					err = netlink.LinkSetDown(link)
					Expect(err).NotTo(HaveOccurred())

					changed, err = EnsureLinkUp(context.Background(), link)
					Expect(err).NotTo(HaveOccurred())
					Expect(changed).To(BeTrue())

					// Verify link is up
					link, err = netlink.LinkByName(nicName)
					Expect(err).NotTo(HaveOccurred())
					Expect(link.Attrs().Flags & net.FlagUp).NotTo(Equal(0))

					return nil
				})
				Expect(err).NotTo(HaveOccurred())
			})

			It("should not change when link is already up", func() {
				var err error
				var changed bool

				err = hostNS.Do(func(netNS ns.NetNS) error {
					defer GinkgoRecover()

					link, err := netlink.LinkByName(nicName)
					Expect(err).NotTo(HaveOccurred())

					// Ensure link is up
					err = netlink.LinkSetUp(link)
					Expect(err).NotTo(HaveOccurred())

					// Read again
					link, err = netlink.LinkByName(nicName)
					Expect(err).NotTo(HaveOccurred())

					changed, err = EnsureLinkUp(context.Background(), link)
					Expect(err).NotTo(HaveOccurred())
					Expect(changed).To(BeFalse())

					return nil
				})
				Expect(err).NotTo(HaveOccurred())
			})
		})

		Describe("EnsureLinkMTU", func() {
			It("should set MTU when different", func() {
				var err error
				var changed bool

				err = hostNS.Do(func(netNS ns.NetNS) error {
					defer GinkgoRecover()

					link, err := netlink.LinkByName(nicName)
					Expect(err).NotTo(HaveOccurred())

					originalMTU := link.Attrs().MTU
					newMTU := 1500
					if originalMTU == newMTU {
						newMTU = 1400
					}

					changed, err = EnsureLinkMTU(context.Background(), link, newMTU)
					Expect(err).NotTo(HaveOccurred())
					Expect(changed).To(BeTrue())

					// Verify MTU is set
					link, err = netlink.LinkByName(nicName)
					Expect(err).NotTo(HaveOccurred())
					Expect(link.Attrs().MTU).To(Equal(newMTU))

					return nil
				})
				Expect(err).NotTo(HaveOccurred())
			})

			It("should not change when MTU is already set", func() {
				var err error
				var changed bool

				err = hostNS.Do(func(netNS ns.NetNS) error {
					defer GinkgoRecover()

					link, err := netlink.LinkByName(nicName)
					Expect(err).NotTo(HaveOccurred())

					currentMTU := link.Attrs().MTU

					changed, err = EnsureLinkMTU(context.Background(), link, currentMTU)
					Expect(err).NotTo(HaveOccurred())
					Expect(changed).To(BeFalse())

					return nil
				})
				Expect(err).NotTo(HaveOccurred())
			})
		})

		Describe("EnsureLinkName", func() {
			It("should rename link when name is different", func() {
				var err error
				var changed bool
				newName := "renamed-nic"

				err = hostNS.Do(func(netNS ns.NetNS) error {
					defer GinkgoRecover()

					link, err := netlink.LinkByName(nicName)
					Expect(err).NotTo(HaveOccurred())

					changed, err = EnsureLinkName(context.Background(), link, newName)
					Expect(err).NotTo(HaveOccurred())
					Expect(changed).To(BeTrue())

					// Verify link is renamed
					_, err = netlink.LinkByName(newName)
					Expect(err).NotTo(HaveOccurred())

					// Verify old name doesn't exist
					_, err = netlink.LinkByName(nicName)
					Expect(err).To(HaveOccurred())

					return nil
				})
				Expect(err).NotTo(HaveOccurred())
			})

			It("should not change when name is already correct", func() {
				var err error
				var changed bool

				err = hostNS.Do(func(netNS ns.NetNS) error {
					defer GinkgoRecover()

					link, err := netlink.LinkByName(nicName)
					Expect(err).NotTo(HaveOccurred())

					changed, err = EnsureLinkName(context.Background(), link, nicName)
					Expect(err).NotTo(HaveOccurred())
					Expect(changed).To(BeFalse())

					return nil
				})
				Expect(err).NotTo(HaveOccurred())
			})
		})

		Describe("EnsureLinkMAC", func() {
			It("should set MAC address when different", func() {
				var err error
				newMAC := "00:11:22:33:44:55"

				err = hostNS.Do(func(netNS ns.NetNS) error {
					defer GinkgoRecover()

					link, err := netlink.LinkByName(nicName)
					Expect(err).NotTo(HaveOccurred())

					err = EnsureLinkMAC(context.Background(), link, newMAC)
					Expect(err).NotTo(HaveOccurred())

					// Verify MAC is set
					link, err = netlink.LinkByName(nicName)
					Expect(err).NotTo(HaveOccurred())
					Expect(link.Attrs().HardwareAddr.String()).To(Equal(newMAC))

					return nil
				})
				Expect(err).NotTo(HaveOccurred())
			})

			It("should not change when MAC is already correct", func() {
				var err error

				err = hostNS.Do(func(netNS ns.NetNS) error {
					defer GinkgoRecover()

					link, err := netlink.LinkByName(nicName)
					Expect(err).NotTo(HaveOccurred())

					currentMAC := link.Attrs().HardwareAddr.String()

					err = EnsureLinkMAC(context.Background(), link, currentMAC)
					Expect(err).NotTo(HaveOccurred())

					return nil
				})
				Expect(err).NotTo(HaveOccurred())
			})

			It("should return error for invalid MAC address", func() {
				var err error
				invalidMAC := "invalid-mac"

				err = hostNS.Do(func(netNS ns.NetNS) error {
					defer GinkgoRecover()

					link, err := netlink.LinkByName(nicName)
					Expect(err).NotTo(HaveOccurred())

					err = EnsureLinkMAC(context.Background(), link, invalidMAC)
					Expect(err).To(HaveOccurred())

					return nil
				})
				Expect(err).NotTo(HaveOccurred())
			})
		})

		Describe("DelLinkByName", func() {
			It("should delete link by name", func() {
				var err error

				err = hostNS.Do(func(netNS ns.NetNS) error {
					defer GinkgoRecover()

					// Verify link exists
					_, err := netlink.LinkByName(nicName)
					Expect(err).NotTo(HaveOccurred())

					err = DelLinkByName(context.Background(), nicName)
					Expect(err).NotTo(HaveOccurred())

					// Verify link is deleted
					_, err = netlink.LinkByName(nicName)
					Expect(err).To(HaveOccurred())

					return nil
				})
				Expect(err).NotTo(HaveOccurred())
			})

			It("should not return error when link doesn't exist", func() {
				var err error

				err = hostNS.Do(func(netNS ns.NetNS) error {
					defer GinkgoRecover()

					err = DelLinkByName(context.Background(), "non-exist")
					Expect(err).NotTo(HaveOccurred())

					return nil
				})
				Expect(err).NotTo(HaveOccurred())
			})
		})

		Describe("EnsureAddr", func() {
			It("should add address when none exists", func() {
				var err error
				var changed bool

				err = hostNS.Do(func(netNS ns.NetNS) error {
					defer GinkgoRecover()

					link, err := netlink.LinkByName(nicName)
					Expect(err).NotTo(HaveOccurred())

					addr := &netlink.Addr{
						IPNet: &net.IPNet{
							IP:   net.ParseIP("192.168.1.100"),
							Mask: net.CIDRMask(24, 32),
						},
						Scope: int(netlink.SCOPE_UNIVERSE),
					}

					changed, err = EnsureAddr(context.Background(), link, addr)
					Expect(err).NotTo(HaveOccurred())
					Expect(changed).To(BeTrue())

					// Verify address is added
					addrList, err := netlink.AddrList(link, netlink.FAMILY_V4)
					Expect(err).NotTo(HaveOccurred())

					found := false
					for _, existingAddr := range addrList {
						if existingAddr.IPNet.String() == addr.IPNet.String() {
							found = true
							break
						}
					}
					Expect(found).To(BeTrue())

					return nil
				})
				Expect(err).NotTo(HaveOccurred())
			})

			It("should replace existing address with different one", func() {
				var err error
				var changed bool

				err = hostNS.Do(func(netNS ns.NetNS) error {
					defer GinkgoRecover()

					link, err := netlink.LinkByName(nicName)
					Expect(err).NotTo(HaveOccurred())

					// Add initial address
					initialAddr := &netlink.Addr{
						IPNet: &net.IPNet{
							IP:   net.ParseIP("192.168.1.100"),
							Mask: net.CIDRMask(24, 32),
						},
						Scope: int(netlink.SCOPE_UNIVERSE),
					}

					err = netlink.AddrAdd(link, initialAddr)
					Expect(err).NotTo(HaveOccurred())

					// Replace with new address
					newAddr := &netlink.Addr{
						IPNet: &net.IPNet{
							IP:   net.ParseIP("192.168.1.200"),
							Mask: net.CIDRMask(24, 32),
						},
						Scope: int(netlink.SCOPE_UNIVERSE),
					}

					changed, err = EnsureAddr(context.Background(), link, newAddr)
					Expect(err).NotTo(HaveOccurred())
					Expect(changed).To(BeTrue())

					// Verify old address is removed and new one is added
					addrList, err := netlink.AddrList(link, netlink.FAMILY_V4)
					Expect(err).NotTo(HaveOccurred())

					oldFound := false
					newFound := false
					for _, existingAddr := range addrList {
						if existingAddr.IPNet.String() == initialAddr.IPNet.String() {
							oldFound = true
						}
						if existingAddr.IPNet.String() == newAddr.IPNet.String() {
							newFound = true
						}
					}
					Expect(oldFound).To(BeFalse())
					Expect(newFound).To(BeTrue())

					return nil
				})
				Expect(err).NotTo(HaveOccurred())
			})

			It("should not change when address already exists", func() {
				var err error
				var changed bool

				err = hostNS.Do(func(netNS ns.NetNS) error {
					defer GinkgoRecover()

					link, err := netlink.LinkByName(nicName)
					Expect(err).NotTo(HaveOccurred())

					addr := &netlink.Addr{
						IPNet: &net.IPNet{
							IP:   net.ParseIP("192.168.1.100"),
							Mask: net.CIDRMask(24, 32),
						},
						Scope: int(netlink.SCOPE_UNIVERSE),
					}

					// Add address first
					err = netlink.AddrAdd(link, addr)
					Expect(err).NotTo(HaveOccurred())

					// Try to ensure same address
					changed, err = EnsureAddr(context.Background(), link, addr)
					Expect(err).NotTo(HaveOccurred())
					Expect(changed).To(BeFalse())

					return nil
				})
				Expect(err).NotTo(HaveOccurred())
			})
		})
	})

	Describe("EnsureHostNsConfig", func() {
		It("should configure host namespace for IPv4", func() {
			err := EnsureHostNsConfig(true, false)
			Expect(err).NotTo(HaveOccurred())
		})

		It("should configure host namespace for IPv6", func() {
			err := EnsureHostNsConfig(false, true)
			Expect(err).NotTo(HaveOccurred())
		})

		It("should configure host namespace for both IPv4 and IPv6", func() {
			err := EnsureHostNsConfig(true, true)
			Expect(err).NotTo(HaveOccurred())
		})
	})

	Describe("IPNet utilities", func() {
		Describe("NewIPNetWithMaxMask", func() {
			It("should create IPv4 net with /32 mask", func() {
				ipNet := &net.IPNet{
					IP:   net.ParseIP("192.168.1.1"),
					Mask: net.CIDRMask(24, 32),
				}

				result := NewIPNetWithMaxMask(ipNet)
				Expect(result.IP.String()).To(Equal("192.168.1.1"))
				Expect(result.Mask.String()).To(Equal("ffffffff"))
			})

			It("should create IPv6 net with /128 mask", func() {
				ipNet := &net.IPNet{
					IP:   net.ParseIP("fd00::1"),
					Mask: net.CIDRMask(64, 128),
				}

				result := NewIPNetWithMaxMask(ipNet)
				Expect(result.IP.String()).To(Equal("fd00::1"))
				Expect(result.Mask.String()).To(Equal("ffffffffffffffffffffffffffffffff"))
			})
		})

		Describe("NewIPNet1", func() {
			It("should create addr list from IPv4 only", func() {
				ipNetSet := &terwayTypes.IPNetSet{
					IPv4: &net.IPNet{
						IP:   net.ParseIP("192.168.1.1"),
						Mask: net.CIDRMask(24, 32),
					},
				}

				addrs := NewIPNet1(ipNetSet)
				Expect(len(addrs)).To(Equal(1))
				Expect(addrs[0].IPNet.String()).To(Equal("192.168.1.1/24"))
			})

			It("should create addr list from IPv6 only", func() {
				ipNetSet := &terwayTypes.IPNetSet{
					IPv6: &net.IPNet{
						IP:   net.ParseIP("fd00::1"),
						Mask: net.CIDRMask(64, 128),
					},
				}

				addrs := NewIPNet1(ipNetSet)
				Expect(len(addrs)).To(Equal(1))
				Expect(addrs[0].IPNet.String()).To(Equal("fd00::1/64"))
			})

			It("should create addr list from both IPv4 and IPv6", func() {
				ipNetSet := &terwayTypes.IPNetSet{
					IPv4: &net.IPNet{
						IP:   net.ParseIP("192.168.1.1"),
						Mask: net.CIDRMask(24, 32),
					},
					IPv6: &net.IPNet{
						IP:   net.ParseIP("fd00::1"),
						Mask: net.CIDRMask(64, 128),
					},
				}

				addrs := NewIPNet1(ipNetSet)
				Expect(len(addrs)).To(Equal(2))
				Expect(addrs[0].IPNet.String()).To(Equal("192.168.1.1/24"))
				Expect(addrs[1].IPNet.String()).To(Equal("fd00::1/64"))
			})
		})

		Describe("NewIPNetToMaxMask", func() {
			It("should create addr list with max mask for IPv4", func() {
				ipNetSet := &terwayTypes.IPNetSet{
					IPv4: &net.IPNet{
						IP:   net.ParseIP("192.168.1.1"),
						Mask: net.CIDRMask(24, 32),
					},
				}

				addrs := NewIPNetToMaxMask(ipNetSet)
				Expect(len(addrs)).To(Equal(1))
				Expect(addrs[0].IPNet.String()).To(Equal("192.168.1.1/32"))
			})

			It("should create addr list with max mask for IPv6", func() {
				ipNetSet := &terwayTypes.IPNetSet{
					IPv6: &net.IPNet{
						IP:   net.ParseIP("fd00::1"),
						Mask: net.CIDRMask(64, 128),
					},
				}

				addrs := NewIPNetToMaxMask(ipNetSet)
				Expect(len(addrs)).To(Equal(1))
				Expect(addrs[0].IPNet.String()).To(Equal("fd00::1/128"))
			})

			It("should return nil for nil input", func() {
				addrs := NewIPNetToMaxMask(nil)
				Expect(addrs).To(BeNil())
			})
		})

		Describe("NewIPNet", func() {
			It("should create IPNetSet with max mask for IPv4", func() {
				ipNetSet := &terwayTypes.IPNetSet{
					IPv4: &net.IPNet{
						IP:   net.ParseIP("192.168.1.1"),
						Mask: net.CIDRMask(24, 32),
					},
				}

				result := NewIPNet(ipNetSet)
				Expect(result.IPv4.String()).To(Equal("192.168.1.1/32"))
				Expect(result.IPv6).To(BeNil())
			})

			It("should create IPNetSet with max mask for IPv6", func() {
				ipNetSet := &terwayTypes.IPNetSet{
					IPv6: &net.IPNet{
						IP:   net.ParseIP("fd00::1"),
						Mask: net.CIDRMask(64, 128),
					},
				}

				result := NewIPNet(ipNetSet)
				Expect(result.IPv6.String()).To(Equal("fd00::1/128"))
				Expect(result.IPv4).To(BeNil())
			})
		})
	})

	Describe("Route operations", func() {
		var hostNS ns.NetNS
		const nicName = "route-test-nic"

		BeforeEach(func() {
			var err error
			hostNS, err = testutils.NewNS()
			Expect(err).NotTo(HaveOccurred())

			err = hostNS.Do(func(netNS ns.NetNS) error {
				defer GinkgoRecover()
				return netlink.LinkAdd(&netlink.Dummy{
					LinkAttrs: netlink.LinkAttrs{Name: nicName},
				})
			})
			Expect(err).NotTo(HaveOccurred())
		})

		AfterEach(func() {
			_ = hostNS.Do(func(netNS ns.NetNS) error {
				defer GinkgoRecover()
				link, err := netlink.LinkByName(nicName)
				if err == nil {
					err = netlink.LinkDel(link)
					Expect(err).NotTo(HaveOccurred())
				}
				return nil
			})
			Expect(hostNS.Close()).To(Succeed())
			Expect(testutils.UnmountNS(hostNS)).To(Succeed())
		})

		Describe("FoundRoutes", func() {
			It("should return error for nil destination", func() {
				var err error

				err = hostNS.Do(func(netNS ns.NetNS) error {
					defer GinkgoRecover()

					route := &netlink.Route{
						Dst: nil,
					}

					_, err := FoundRoutes(route)
					Expect(err).To(HaveOccurred())

					return nil
				})
				Expect(err).NotTo(HaveOccurred())
			})
		})
	})

	Describe("IP Rule operations", func() {
		var hostNS ns.NetNS
		const nicName = "rule-test-nic"

		BeforeEach(func() {
			var err error
			hostNS, err = testutils.NewNS()
			Expect(err).NotTo(HaveOccurred())

			err = hostNS.Do(func(netNS ns.NetNS) error {
				defer GinkgoRecover()
				return netlink.LinkAdd(&netlink.Dummy{
					LinkAttrs: netlink.LinkAttrs{Name: nicName},
				})
			})
			Expect(err).NotTo(HaveOccurred())
		})

		AfterEach(func() {
			_ = hostNS.Do(func(netNS ns.NetNS) error {
				defer GinkgoRecover()
				link, err := netlink.LinkByName(nicName)
				if err == nil {
					err = netlink.LinkDel(link)
					Expect(err).NotTo(HaveOccurred())
				}
				return nil
			})
			Expect(hostNS.Close()).To(Succeed())
			Expect(testutils.UnmountNS(hostNS)).To(Succeed())
		})

		Describe("FindIPRule", func() {
			It("should find rule with source IP", func() {
				var err error

				err = hostNS.Do(func(netNS ns.NetNS) error {
					defer GinkgoRecover()

					// Add a rule first
					rule := netlink.NewRule()
					rule.Src = &net.IPNet{
						IP:   net.ParseIP("192.168.1.0"),
						Mask: net.CIDRMask(24, 32),
					}
					rule.Table = 100
					rule.Priority = 1000

					err = netlink.RuleAdd(rule)
					Expect(err).NotTo(HaveOccurred())

					// Find the rule
					rules, err := FindIPRule(rule)
					Expect(err).NotTo(HaveOccurred())
					Expect(len(rules)).To(BeNumerically(">", 0))

					return nil
				})
				Expect(err).NotTo(HaveOccurred())
			})

			It("should return error for empty rule", func() {
				var err error

				err = hostNS.Do(func(netNS ns.NetNS) error {
					defer GinkgoRecover()

					rule := &netlink.Rule{}

					_, err := FindIPRule(rule)
					Expect(err).To(HaveOccurred())

					return nil
				})
				Expect(err).NotTo(HaveOccurred())
			})
		})

		Describe("EnsureIPRule", func() {
			It("should add rule when not exists", func() {
				var err error
				var changed bool

				err = hostNS.Do(func(netNS ns.NetNS) error {
					defer GinkgoRecover()

					rule := netlink.NewRule()
					rule.Src = &net.IPNet{
						IP:   net.ParseIP("192.168.5.0"),
						Mask: net.CIDRMask(24, 32),
					}
					rule.Table = 200
					rule.Priority = 2000

					changed, err = EnsureIPRule(context.Background(), rule)
					Expect(err).NotTo(HaveOccurred())
					Expect(changed).To(BeTrue())

					// Verify rule is added
					rules, err := FindIPRule(rule)
					Expect(err).NotTo(HaveOccurred())
					Expect(len(rules)).To(BeNumerically(">", 0))

					return nil
				})
				Expect(err).NotTo(HaveOccurred())
			})
		})
	})

	Describe("GenerateIPv6Sysctl", func() {
		It("should generate sysctl config for interface", func() {
			result := GenerateIPv6Sysctl("eth0", true, true)

			Expect(result).To(HaveKey("/proc/sys/net/ipv6/conf/lo/disable_ipv6"))
			Expect(result).To(HaveKey("/proc/sys/net/ipv6/conf/all/disable_ipv6"))
			Expect(result).To(HaveKey("/proc/sys/net/ipv6/conf/default/disable_ipv6"))
			Expect(result).To(HaveKey("/proc/sys/net/ipv6/conf/eth0/disable_ipv6"))
			Expect(result).To(HaveKey("/proc/sys/net/ipv6/conf/eth0/accept_ra"))
			Expect(result).To(HaveKey("/proc/sys/net/ipv6/conf/eth0/forwarding"))
		})

		It("should generate sysctl config without interface", func() {
			result := GenerateIPv6Sysctl("", false, false)

			Expect(result).To(HaveKey("/proc/sys/net/ipv6/conf/lo/disable_ipv6"))
			Expect(result).To(HaveKey("/proc/sys/net/ipv6/conf/all/disable_ipv6"))
			Expect(result).To(HaveKey("/proc/sys/net/ipv6/conf/default/disable_ipv6"))
			Expect(result).NotTo(HaveKey("/proc/sys/net/ipv6/conf/eth0/disable_ipv6"))
		})
	})

	Describe("EnsureNeigh", func() {
		var hostNS ns.NetNS
		const nicName = "neigh-test-nic"

		BeforeEach(func() {
			var err error
			hostNS, err = testutils.NewNS()
			Expect(err).NotTo(HaveOccurred())

			err = hostNS.Do(func(netNS ns.NetNS) error {
				defer GinkgoRecover()
				return netlink.LinkAdd(&netlink.Dummy{
					LinkAttrs: netlink.LinkAttrs{Name: nicName},
				})
			})
			Expect(err).NotTo(HaveOccurred())
		})

		AfterEach(func() {
			_ = hostNS.Do(func(netNS ns.NetNS) error {
				defer GinkgoRecover()
				link, err := netlink.LinkByName(nicName)
				if err == nil {
					err = netlink.LinkDel(link)
					Expect(err).NotTo(HaveOccurred())
				}
				return nil
			})
			Expect(hostNS.Close()).To(Succeed())
			Expect(testutils.UnmountNS(hostNS)).To(Succeed())
		})

		It("should add neighbor entry when not exists", func() {
			var err error
			var changed bool

			err = hostNS.Do(func(netNS ns.NetNS) error {
				defer GinkgoRecover()

				link, err := netlink.LinkByName(nicName)
				Expect(err).NotTo(HaveOccurred())

				neigh := &netlink.Neigh{
					IP:           net.ParseIP("192.168.1.100"),
					HardwareAddr: net.HardwareAddr{0x00, 0x11, 0x22, 0x33, 0x44, 0x55},
					LinkIndex:    link.Attrs().Index,
					State:        netlink.NUD_PERMANENT,
				}

				changed, err = EnsureNeigh(context.Background(), neigh)
				Expect(err).NotTo(HaveOccurred())
				Expect(changed).To(BeTrue())

				// Verify neighbor is added
				neighs, err := netlink.NeighList(link.Attrs().Index, netlink.FAMILY_V4)
				Expect(err).NotTo(HaveOccurred())

				found := false
				for _, n := range neighs {
					if n.IP.Equal(neigh.IP) && n.HardwareAddr.String() == neigh.HardwareAddr.String() {
						found = true
						break
					}
				}
				Expect(found).To(BeTrue())

				return nil
			})
			Expect(err).NotTo(HaveOccurred())
		})

		It("should not add neighbor when already exists", func() {
			var err error
			var changed bool

			err = hostNS.Do(func(netNS ns.NetNS) error {
				defer GinkgoRecover()

				link, err := netlink.LinkByName(nicName)
				Expect(err).NotTo(HaveOccurred())

				neigh := &netlink.Neigh{
					IP:           net.ParseIP("192.168.1.200"),
					HardwareAddr: net.HardwareAddr{0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff},
					LinkIndex:    link.Attrs().Index,
					State:        netlink.NUD_PERMANENT,
				}

				// Add neighbor first
				err = netlink.NeighSet(neigh)
				Expect(err).NotTo(HaveOccurred())

				// Try to ensure same neighbor
				changed, err = EnsureNeigh(context.Background(), neigh)
				Expect(err).NotTo(HaveOccurred())
				Expect(changed).To(BeFalse())

				return nil
			})
			Expect(err).NotTo(HaveOccurred())
		})
	})

	Describe("TC filter operations", func() {
		var hostNS ns.NetNS
		const nicName = "eni"

		BeforeEach(func() {
			var err error
			hostNS, err = testutils.NewNS()
			Expect(err).NotTo(HaveOccurred())

			err = hostNS.Do(func(netNS ns.NetNS) error {
				defer GinkgoRecover()
				return netlink.LinkAdd(&netlink.GenericLink{
					LinkAttrs: netlink.LinkAttrs{
						Name:        nicName,
						TxQLen:      1000,
						NumTxQueues: 2,
						NumRxQueues: 2,
					},
					LinkType: "veth",
				})
			})
			Expect(err).NotTo(HaveOccurred())
		})

		AfterEach(func() {
			_ = hostNS.Do(func(netNS ns.NetNS) error {
				defer GinkgoRecover()
				eni, err := netlink.LinkByName(nicName)
				Expect(err).NotTo(HaveOccurred())
				err = netlink.LinkDel(eni)
				Expect(err).NotTo(HaveOccurred())
				return nil
			})
			Expect(hostNS.Close()).To(Succeed())
			Expect(testutils.UnmountNS(hostNS)).To(Succeed())
		})

		It("should add qdisc mq", func() {
			var err error
			err = hostNS.Do(func(netNS ns.NetNS) error {
				defer GinkgoRecover()
				eni, err := netlink.LinkByName(nicName)
				Expect(err).NotTo(HaveOccurred())

				err = EnsureMQQdisc(context.Background(), eni)
				Expect(err).NotTo(HaveOccurred())

				qds, err := netlink.QdiscList(eni)
				Expect(err).NotTo(HaveOccurred())

				found := false
				for _, qd := range qds {
					if qd.Type() != "mq" {
						continue
					}
					Expect(qd.Attrs().Parent).Should(Equal(uint32(netlink.HANDLE_ROOT)))
					Expect(qd.Attrs().Handle).Should(Equal(netlink.MakeHandle(1, 0)))
					found = true
					break
				}
				Expect(found).Should(BeTrue())
				return nil
			})
			Expect(err).NotTo(HaveOccurred())
		})

		It("should set egress filter", func() {
			var err error
			err = hostNS.Do(func(netNS ns.NetNS) error {
				defer GinkgoRecover()

				eni, err := netlink.LinkByName(nicName)
				Expect(err).NotTo(HaveOccurred())

				err = SetEgressPriority(context.Background(), eni, netlink.MakeHandle(1, 1), &terwayTypes.IPNetSet{
					IPv4: &net.IPNet{
						IP:   net.ParseIP("192.168.1.1"),
						Mask: net.CIDRMask(32, 32),
					},
					IPv6: &net.IPNet{
						IP:   net.ParseIP("fd00::1"),
						Mask: net.CIDRMask(128, 128),
					},
				})
				Expect(err).NotTo(HaveOccurred())

				qds, err := netlink.QdiscList(eni)
				Expect(err).NotTo(HaveOccurred())

				for _, qd := range qds {
					_, ok := qd.(*netlink.Prio)
					if !ok {
						continue
					}

					u32, err := tc.FilterBySrcIP(eni, qd.Attrs().Handle, &net.IPNet{
						IP:   net.ParseIP("192.168.1.1"),
						Mask: net.CIDRMask(32, 32),
					})
					Expect(err).NotTo(HaveOccurred())
					Expect(u32).NotTo(BeNil(), "tc filter with src ipv4 should be found. Qdisc %#v", qd)

					u32v6, err := tc.FilterBySrcIP(eni, qd.Attrs().Handle, &net.IPNet{
						IP:   net.ParseIP("fd00::1"),
						Mask: net.CIDRMask(128, 128),
					})
					Expect(err).NotTo(HaveOccurred())
					Expect(u32v6).NotTo(BeNil(), "tc filter with src ipv6 should be found")
				}
				return nil
			})
			Expect(err).NotTo(HaveOccurred())
		})

		It("should add vlan tag", func() {
			var err error
			err = hostNS.Do(func(netNS ns.NetNS) error {
				defer GinkgoRecover()
				eni, err := netlink.LinkByName(nicName)
				Expect(err).NotTo(HaveOccurred())

				By("add new ip", func() {
					err = EnsureVlanTag(context.Background(), eni, &terwayTypes.IPNetSet{
						IPv4: &net.IPNet{
							IP:   net.ParseIP("192.168.1.1"),
							Mask: net.CIDRMask(32, 32),
						},
						IPv6: &net.IPNet{
							IP:   net.ParseIP("fd00::1"),
							Mask: net.CIDRMask(128, 128),
						},
					}, 1023)
					Expect(err).NotTo(HaveOccurred())

					filters, err := netlink.FilterList(eni, netlink.HANDLE_MIN_EGRESS)
					Expect(err).NotTo(HaveOccurred())

					Expect(len(filters)).Should(Equal(2))
				})

				By("re-add same ip should succeed", func() {
					err = EnsureVlanTag(context.Background(), eni, &terwayTypes.IPNetSet{
						IPv4: &net.IPNet{
							IP:   net.ParseIP("192.168.1.1"),
							Mask: net.CIDRMask(32, 32),
						},
						IPv6: &net.IPNet{
							IP:   net.ParseIP("fd00::1"),
							Mask: net.CIDRMask(128, 128),
						},
					}, 1023)
					Expect(err).NotTo(HaveOccurred())

					filters, err := netlink.FilterList(eni, netlink.HANDLE_MIN_EGRESS)
					Expect(err).NotTo(HaveOccurred())

					Expect(len(filters)).Should(Equal(2))
				})

				By("add new rule should success", func() {
					err = EnsureVlanTag(context.Background(), eni, &terwayTypes.IPNetSet{
						IPv4: &net.IPNet{
							IP:   net.ParseIP("192.168.1.2"),
							Mask: net.CIDRMask(32, 32),
						},
						IPv6: &net.IPNet{
							IP:   net.ParseIP("fd00::2"),
							Mask: net.CIDRMask(128, 128),
						},
					}, 1022)
					Expect(err).NotTo(HaveOccurred())

					filters, err := netlink.FilterList(eni, netlink.HANDLE_MIN_EGRESS)
					Expect(err).NotTo(HaveOccurred())

					Expect(len(filters)).Should(Equal(4))
				})

				By("delete filter should clean up", func() {
					err = DelFilter(context.Background(), eni, netlink.HANDLE_MIN_EGRESS, &terwayTypes.IPNetSet{
						IPv4: &net.IPNet{
							IP:   net.ParseIP("192.168.1.2"),
							Mask: net.CIDRMask(32, 32),
						},
						IPv6: &net.IPNet{
							IP:   net.ParseIP("fd00::2"),
							Mask: net.CIDRMask(128, 128),
						},
					})
					Expect(err).NotTo(HaveOccurred())

					filters, err := netlink.FilterList(eni, netlink.HANDLE_MIN_EGRESS)
					Expect(err).NotTo(HaveOccurred())

					Expect(len(filters)).Should(Equal(2))
				})

				By("add same ip vid should be updated", func() {
					err = EnsureVlanTag(context.Background(), eni, &terwayTypes.IPNetSet{
						IPv4: &net.IPNet{
							IP:   net.ParseIP("192.168.1.1"),
							Mask: net.CIDRMask(32, 32),
						},
						IPv6: &net.IPNet{
							IP:   net.ParseIP("fd00::1"),
							Mask: net.CIDRMask(128, 128),
						},
					}, 4000)
					Expect(err).NotTo(HaveOccurred())

					filters, err := netlink.FilterList(eni, netlink.HANDLE_MIN_EGRESS)
					Expect(err).NotTo(HaveOccurred())

					Expect(len(filters)).Should(Equal(2))

					for _, filter := range filters {
						u32 := filter.(*netlink.U32)
						Expect(len(u32.Actions)).Should(Equal(1))

						act := u32.Actions[0].(*netlink.VlanAction)

						Expect(act.Attrs().Action).Should(Equal(netlink.TC_ACT_PIPE))
						Expect(act.Action).Should(Equal(netlink.TCA_VLAN_KEY_PUSH))
						Expect(act.Vid).Should(Equal(uint16(4000)))
					}
				})
				return nil
			})
			Expect(err).NotTo(HaveOccurred())
		})
	})

	Describe("ERdma link operations", func() {
		It("should parse ERdma link hardware address correctly", func() {
			hwaddr, err := parseERdmaLinkHwAddr("0d:d3:04:fe:ff:3e:16:02")
			Expect(err).NotTo(HaveOccurred())
			Expect(hwaddr.String()).To(Equal("02:16:3e:04:d3:0d"))
		})
	})
})
