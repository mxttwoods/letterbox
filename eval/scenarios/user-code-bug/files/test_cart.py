import unittest

from cart import total


class CartTest(unittest.TestCase):
    def test_no_discount(self):
        self.assertEqual(total([10, 5]), 15)

    def test_discount_applied_once(self):
        self.assertEqual(total([100], discount=0.1), 90)


if __name__ == "__main__":
    unittest.main()
